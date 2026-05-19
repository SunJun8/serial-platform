package rfc2217

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type CommandKind int

const (
	CommandSetBaudrate CommandKind = 1
	CommandSetDataBits CommandKind = 2
	CommandSetParity   CommandKind = 3
	CommandSetStopBits CommandKind = 4
	CommandSetDTR      CommandKind = 5
	CommandSetRTS      CommandKind = 6
	CommandSetBreak    CommandKind = 7
	CommandSetControl  CommandKind = 8
)

type Command struct {
	Kind      CommandKind
	Baudrate  int
	IntValue  int
	BoolValue bool
	Query     bool
}

type OperationKind int

const (
	OperationData OperationKind = iota + 1
	OperationCommand
	OperationReply
)

type Operation struct {
	Kind    OperationKind
	Data    []byte
	Command Command
}

type Parser struct {
	pending []byte
}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Feed(in []byte) ([]Operation, error) {
	if len(in) > 0 {
		p.pending = append(p.pending, in...)
	}
	ops, consumed, err := parseOperations(p.pending)
	if err != nil {
		return nil, err
	}
	if consumed > 0 {
		copy(p.pending, p.pending[consumed:])
		p.pending = p.pending[:len(p.pending)-consumed]
	}
	return ops, nil
}

func (p *Parser) Close() error {
	if len(p.pending) == 0 {
		return nil
	}
	return incompleteError(p.pending)
}

func ParseClientBytes(in []byte) ([]Command, []byte, error) {
	parser := NewParser()
	ops, err := parser.Feed(in)
	if err != nil {
		return nil, nil, err
	}
	if err := parser.Close(); err != nil {
		return nil, nil, err
	}

	commands := make([]Command, 0)
	data := make([]byte, 0, len(in))
	for _, op := range ops {
		switch op.Kind {
		case OperationCommand:
			commands = append(commands, op.Command)
		case OperationData:
			data = append(data, op.Data...)
		}
	}
	return commands, data, nil
}

func parseOperations(in []byte) ([]Operation, int, error) {
	ops := make([]Operation, 0)
	data := make([]byte, 0, len(in))
	i := 0

	flushData := func() {
		if len(data) == 0 {
			return
		}
		ops = append(ops, Operation{Kind: OperationData, Data: append([]byte(nil), data...)})
		data = data[:0]
	}

	for i < len(in) {
		if in[i] != IAC {
			data = append(data, in[i])
			i++
			continue
		}
		if i+1 >= len(in) {
			break
		}
		if in[i+1] == IAC {
			data = append(data, IAC)
			i += 2
			continue
		}
		if isTelnetOptionCommand(in[i+1]) {
			if i+2 >= len(in) {
				break
			}
			reply := negotiationReply(in[i+1], in[i+2])
			if len(reply) > 0 {
				flushData()
				ops = append(ops, Operation{Kind: OperationReply, Data: reply})
			}
			i += 3
			continue
		}
		if in[i+1] != SB {
			i += 2
			continue
		}

		end := findSubnegotiationEnd(in, i+2)
		if end < 0 {
			break
		}
		command, ok, err := parseSubnegotiation(unescapeIAC(in[i+2 : end]))
		if err != nil {
			return nil, 0, err
		}
		if ok {
			flushData()
			ops = append(ops, Operation{Kind: OperationCommand, Command: command})
		}
		i = end + 2
	}

	flushData()
	return ops, i, nil
}

func isTelnetOptionCommand(command byte) bool {
	return command == WILL || command == WONT || command == DO || command == DONT
}

func negotiationReply(command, option byte) []byte {
	if option == COMPortOption {
		switch command {
		case WILL:
			return []byte{IAC, DO, option}
		case DO:
			return []byte{IAC, WILL, option}
		case WONT:
			return []byte{IAC, DONT, option}
		case DONT:
			return []byte{IAC, WONT, option}
		}
	}

	switch command {
	case WILL, WONT:
		return []byte{IAC, DONT, option}
	case DO, DONT:
		return []byte{IAC, WONT, option}
	default:
		return nil
	}
}

func findSubnegotiationEnd(in []byte, start int) int {
	for i := start; i+1 < len(in); i++ {
		if in[i] != IAC {
			continue
		}
		if in[i+1] == IAC {
			i++
			continue
		}
		if in[i+1] == SE {
			return i
		}
	}
	return -1
}

func unescapeIAC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		out = append(out, in[i])
		if in[i] == IAC && i+1 < len(in) && in[i+1] == IAC {
			i++
		}
	}
	return out
}

func parseSubnegotiation(payload []byte) (Command, bool, error) {
	if len(payload) < 2 || payload[0] != COMPortOption {
		return Command{}, false, nil
	}

	switch payload[1] {
	case SetBaudrate:
		if len(payload) != 6 {
			return Command{}, false, errors.New("invalid SET-BAUDRATE length")
		}
		baudrate := int(binary.BigEndian.Uint32(payload[2:6]))
		return Command{Kind: CommandSetBaudrate, Baudrate: baudrate, Query: baudrate == 0}, true, nil
	case SetDataSize:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-DATASIZE length")
		}
		return Command{Kind: CommandSetDataBits, IntValue: int(payload[2]), Query: payload[2] == 0}, true, nil
	case SetParity:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-PARITY length")
		}
		return Command{Kind: CommandSetParity, IntValue: int(payload[2]), Query: payload[2] == 0}, true, nil
	case SetStopSize:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-STOPSIZE length")
		}
		return Command{Kind: CommandSetStopBits, IntValue: int(payload[2]), Query: payload[2] == 0}, true, nil
	case SetControl:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-CONTROL length")
		}
		return parseSetControl(payload[2])
	default:
		return Command{}, false, nil
	}
}

func parseSetControl(value byte) (Command, bool, error) {
	switch value {
	case 0:
		return Command{Kind: CommandSetControl, IntValue: int(value), Query: true}, true, nil
	case ControlBreakStateRequest:
		return Command{Kind: CommandSetBreak, IntValue: int(value), Query: true}, true, nil
	case ControlBreakStateOn:
		return Command{Kind: CommandSetBreak, IntValue: int(value), BoolValue: true}, true, nil
	case ControlBreakStateOff:
		return Command{Kind: CommandSetBreak, IntValue: int(value), BoolValue: false}, true, nil
	case ControlDTRStateRequest:
		return Command{Kind: CommandSetDTR, IntValue: int(value), Query: true}, true, nil
	case ControlDTRStateOn:
		return Command{Kind: CommandSetDTR, IntValue: int(value), BoolValue: true}, true, nil
	case ControlDTRStateOff:
		return Command{Kind: CommandSetDTR, IntValue: int(value), BoolValue: false}, true, nil
	case ControlRTSStateRequest:
		return Command{Kind: CommandSetRTS, IntValue: int(value), Query: true}, true, nil
	case ControlRTSStateOn:
		return Command{Kind: CommandSetRTS, IntValue: int(value), BoolValue: true}, true, nil
	case ControlRTSStateOff:
		return Command{Kind: CommandSetRTS, IntValue: int(value), BoolValue: false}, true, nil
	default:
		return Command{}, false, nil
	}
}

func incompleteError(pending []byte) error {
	if len(pending) == 0 {
		return nil
	}
	if pending[0] != IAC {
		return fmt.Errorf("unconsumed parser data: %x", pending)
	}
	if len(pending) == 1 {
		return errors.New("truncated telnet command")
	}
	if isTelnetOptionCommand(pending[1]) {
		return errors.New("truncated telnet option command")
	}
	if pending[1] == SB {
		return errors.New("unterminated subnegotiation")
	}
	return errors.New("truncated telnet command")
}
