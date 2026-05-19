package rfc2217

import (
	"encoding/binary"
	"fmt"
	"time"

	"serial-platform/internal/serial"
)

const breakDuration = 250 * time.Millisecond

func Apply(session serial.ControlSession, config serial.Config, commands []Command, data []byte) (serial.Config, error) {
	ops := make([]Operation, 0, len(commands)+1)
	for _, command := range commands {
		ops = append(ops, Operation{Kind: OperationCommand, Command: command})
	}
	if len(data) > 0 {
		ops = append(ops, Operation{Kind: OperationData, Data: data})
	}
	current, _, err := ApplyOperations(session, config, ops)
	return current, err
}

func ApplyOperations(session serial.ControlSession, config serial.Config, ops []Operation) (serial.Config, []byte, error) {
	current := config
	response := make([]byte, 0)

	for _, op := range ops {
		switch op.Kind {
		case OperationData:
			if len(op.Data) > 0 {
				if err := session.Write(op.Data); err != nil {
					return current, response, err
				}
			}
			continue
		case OperationReply:
			response = append(response, op.Data...)
			continue
		case OperationCommand:
		default:
			continue
		}

		command := op.Command
		next := current
		changedConfig := false

		if command.Query {
			confirmation, err := confirmationResponse(current, command)
			if err != nil {
				return current, response, err
			}
			response = append(response, confirmation...)
			continue
		}

		switch command.Kind {
		case CommandSetBaudrate:
			next.Baud = command.Baudrate
			changedConfig = true
		case CommandSetDataBits:
			next.DataBits = command.IntValue
			changedConfig = true
		case CommandSetParity:
			parity, err := parityValue(command.IntValue)
			if err != nil {
				return current, response, err
			}
			next.Parity = parity
			changedConfig = true
		case CommandSetStopBits:
			stopBits, err := stopBitsValue(command.IntValue)
			if err != nil {
				return current, response, err
			}
			next.StopBits = stopBits
			changedConfig = true
		case CommandSetDTR:
			if err := session.SetDTR(command.BoolValue); err != nil {
				return current, response, err
			}
		case CommandSetRTS:
			if err := session.SetRTS(command.BoolValue); err != nil {
				return current, response, err
			}
		case CommandSetBreak:
			duration := time.Duration(0)
			if command.BoolValue {
				duration = breakDuration
			}
			if err := session.SendBreak(duration); err != nil {
				return current, response, err
			}
		}

		if changedConfig {
			if err := session.SetConfig(next); err != nil {
				return current, response, err
			}
			current = next
		}

		confirmation, err := confirmationResponse(current, command)
		if err != nil {
			return current, response, err
		}
		response = append(response, confirmation...)
	}
	return current, response, nil
}

func parityValue(value int) (string, error) {
	switch value {
	case 1:
		return "N", nil
	case 2:
		return "O", nil
	case 3:
		return "E", nil
	case 4:
		return "M", nil
	case 5:
		return "S", nil
	default:
		return "", fmt.Errorf("unsupported RFC2217 parity value %d", value)
	}
}

func parityCode(value string) (byte, error) {
	switch value {
	case "N":
		return 1, nil
	case "O":
		return 2, nil
	case "E":
		return 3, nil
	case "M":
		return 4, nil
	case "S":
		return 5, nil
	default:
		return 0, fmt.Errorf("unsupported serial parity value %q", value)
	}
}

func stopBitsValue(value int) (int, error) {
	switch value {
	case 1:
		return 1, nil
	case 2:
		return 2, nil
	default:
		return 0, fmt.Errorf("unsupported RFC2217 stop bits value %d", value)
	}
}

func confirmationResponse(config serial.Config, command Command) ([]byte, error) {
	switch command.Kind {
	case CommandSetBaudrate:
		var value [4]byte
		binary.BigEndian.PutUint32(value[:], uint32(config.Baud))
		return buildServerSubnegotiation(SetBaudrate, value[:]), nil
	case CommandSetDataBits:
		return buildServerSubnegotiation(SetDataSize, []byte{byte(config.DataBits)}), nil
	case CommandSetParity:
		value, err := parityCode(config.Parity)
		if err != nil {
			return nil, err
		}
		return buildServerSubnegotiation(SetParity, []byte{value}), nil
	case CommandSetStopBits:
		if config.StopBits != 1 && config.StopBits != 2 {
			return nil, fmt.Errorf("unsupported serial stop bits value %d", config.StopBits)
		}
		return buildServerSubnegotiation(SetStopSize, []byte{byte(config.StopBits)}), nil
	case CommandSetControl, CommandSetDTR, CommandSetRTS, CommandSetBreak:
		return buildServerSubnegotiation(SetControl, []byte{byte(command.IntValue)}), nil
	default:
		return nil, nil
	}
}

func buildServerSubnegotiation(command byte, value []byte) []byte {
	out := []byte{IAC, SB, COMPortOption, command + 100}
	out = append(out, value...)
	out = append(out, IAC, SE)
	return out
}
