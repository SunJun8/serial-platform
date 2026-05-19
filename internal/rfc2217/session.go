package rfc2217

import (
	"fmt"
	"time"

	"serial-platform/internal/serial"
)

const breakDuration = 250 * time.Millisecond

func Apply(session serial.ControlSession, config serial.Config, commands []Command, data []byte) (serial.Config, error) {
	current := config
	for _, command := range commands {
		next := current
		changedConfig := false

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
				return current, err
			}
			next.Parity = parity
			changedConfig = true
		case CommandSetStopBits:
			stopBits, err := stopBitsValue(command.IntValue)
			if err != nil {
				return current, err
			}
			next.StopBits = stopBits
			changedConfig = true
		case CommandSetDTR:
			if err := session.SetDTR(command.BoolValue); err != nil {
				return current, err
			}
		case CommandSetRTS:
			if err := session.SetRTS(command.BoolValue); err != nil {
				return current, err
			}
		case CommandSetBreak:
			duration := time.Duration(0)
			if command.BoolValue {
				duration = breakDuration
			}
			if err := session.SendBreak(duration); err != nil {
				return current, err
			}
		}

		if changedConfig {
			if err := session.SetConfig(next); err != nil {
				return current, err
			}
			current = next
		}
	}

	if len(data) > 0 {
		if err := session.Write(data); err != nil {
			return current, err
		}
	}
	return current, nil
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
