package rfc2217

import (
	"encoding/binary"
	"errors"
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
)

type Command struct {
	Kind      CommandKind
	Baudrate  int
	IntValue  int
	BoolValue bool
}

func ParseClientBytes(in []byte) ([]Command, []byte, error) {
	commands := make([]Command, 0)
	data := make([]byte, 0, len(in))

	for i := 0; i < len(in); {
		if in[i] != IAC {
			data = append(data, in[i])
			i++
			continue
		}
		if i+1 >= len(in) {
			return nil, nil, errors.New("truncated telnet command")
		}
		if in[i+1] == IAC {
			data = append(data, IAC)
			i += 2
			continue
		}
		if isTelnetOptionCommand(in[i+1]) {
			if i+2 >= len(in) {
				return nil, nil, errors.New("truncated telnet option command")
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
			return nil, nil, errors.New("unterminated subnegotiation")
		}
		command, ok, err := parseSubnegotiation(in[i+2 : end])
		if err != nil {
			return nil, nil, err
		}
		if ok {
			commands = append(commands, command)
		}
		i = end + 2
	}

	return commands, data, nil
}

func isTelnetOptionCommand(command byte) bool {
	return command == WILL || command == WONT || command == DO || command == DONT
}

func findSubnegotiationEnd(in []byte, start int) int {
	for i := start; i+1 < len(in); i++ {
		if in[i] == IAC && in[i+1] == SE {
			return i
		}
	}
	return -1
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
		return Command{Kind: CommandSetBaudrate, Baudrate: int(binary.BigEndian.Uint32(payload[2:6]))}, true, nil
	case SetDataSize:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-DATASIZE length")
		}
		return Command{Kind: CommandSetDataBits, IntValue: int(payload[2])}, true, nil
	case SetParity:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-PARITY length")
		}
		return Command{Kind: CommandSetParity, IntValue: int(payload[2])}, true, nil
	case SetStopSize:
		if len(payload) != 3 {
			return Command{}, false, errors.New("invalid SET-STOPSIZE length")
		}
		return Command{Kind: CommandSetStopBits, IntValue: int(payload[2])}, true, nil
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
	case ControlBreakStateOn:
		return Command{Kind: CommandSetBreak, BoolValue: true}, true, nil
	case ControlBreakStateOff:
		return Command{Kind: CommandSetBreak, BoolValue: false}, true, nil
	case ControlDTRStateOn:
		return Command{Kind: CommandSetDTR, BoolValue: true}, true, nil
	case ControlDTRStateOff:
		return Command{Kind: CommandSetDTR, BoolValue: false}, true, nil
	case ControlRTSStateOn:
		return Command{Kind: CommandSetRTS, BoolValue: true}, true, nil
	case ControlRTSStateOff:
		return Command{Kind: CommandSetRTS, BoolValue: false}, true, nil
	default:
		return Command{}, false, nil
	}
}
