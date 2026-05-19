package rfc2217

import (
	"bytes"
	"testing"
	"time"

	"serial-platform/internal/serial"
)

func TestParseSetBaudrate(t *testing.T) {
	commands, data, err := ParseClientBytes([]byte{
		IAC, SB, COMPortOption, SetBaudrate,
		0x00, 0x1c, 0x20, 0x00,
		IAC, SE,
	})
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("data = %x, want empty", data)
	}
	if len(commands) != 1 {
		t.Fatalf("len(commands) = %d, want 1", len(commands))
	}
	if commands[0].Kind != CommandSetBaudrate {
		t.Fatalf("command kind = %v, want %v", commands[0].Kind, CommandSetBaudrate)
	}
	if commands[0].Baudrate != 1843200 {
		t.Fatalf("baudrate = %d, want 1843200", commands[0].Baudrate)
	}
}

func TestParseSetDTRAndRTS(t *testing.T) {
	commands, data, err := ParseClientBytes([]byte{
		IAC, SB, COMPortOption, SetControl, ControlDTRStateOn, IAC, SE,
		IAC, SB, COMPortOption, SetControl, ControlRTSStateOff, IAC, SE,
	})
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("data = %x, want empty", data)
	}
	if len(commands) != 2 {
		t.Fatalf("len(commands) = %d, want 2", len(commands))
	}
	if commands[0].Kind != CommandSetDTR || !commands[0].BoolValue {
		t.Fatalf("commands[0] = %+v, want DTR on", commands[0])
	}
	if commands[1].Kind != CommandSetRTS || commands[1].BoolValue {
		t.Fatalf("commands[1] = %+v, want RTS off", commands[1])
	}
}

func TestParsePassThroughData(t *testing.T) {
	input := []byte("console data")

	commands, data, err := ParseClientBytes(input)
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %+v, want empty", commands)
	}
	if !bytes.Equal(data, input) {
		t.Fatalf("data = %q, want %q", data, input)
	}
}

func TestParseSkipsTelnetOptionNegotiation(t *testing.T) {
	commands, data, err := ParseClientBytes([]byte{'a', IAC, DO, COMPortOption, 'b'})
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %+v, want empty", commands)
	}
	if !bytes.Equal(data, []byte("ab")) {
		t.Fatalf("data = %q, want %q", data, "ab")
	}
}

func TestParseRejectsUnterminatedSubnegotiation(t *testing.T) {
	_, _, err := ParseClientBytes([]byte{IAC, SB, COMPortOption, SetBaudrate, 0x00})
	if err == nil {
		t.Fatal("ParseClientBytes returned nil error, want unterminated subnegotiation error")
	}
}

func TestParseEscapedIACData(t *testing.T) {
	commands, data, err := ParseClientBytes([]byte{'a', IAC, IAC, 'b'})
	if err != nil {
		t.Fatalf("ParseClientBytes returned error: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %+v, want empty", commands)
	}
	want := []byte{'a', IAC, 'b'}
	if !bytes.Equal(data, want) {
		t.Fatalf("data = %x, want %x", data, want)
	}
}

func TestApplyWritesDataAndUpdatesControls(t *testing.T) {
	session := &recordingSession{}
	config := serial.DefaultConfig()
	commands := []Command{
		{Kind: CommandSetBaudrate, Baudrate: 921600},
		{Kind: CommandSetDTR, BoolValue: true},
		{Kind: CommandSetRTS, BoolValue: false},
	}

	got, err := Apply(session, config, commands, []byte("show version\r"))
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if got.Baud != 921600 {
		t.Fatalf("baud = %d, want 921600", got.Baud)
	}
	if len(session.configs) != 1 || session.configs[0].Baud != 921600 {
		t.Fatalf("configs = %+v, want one baud update", session.configs)
	}
	if !session.dtrSet || !session.dtr {
		t.Fatalf("DTR set = %v value = %v, want set true", session.dtrSet, session.dtr)
	}
	if !session.rtsSet || session.rts {
		t.Fatalf("RTS set = %v value = %v, want set false", session.rtsSet, session.rts)
	}
	if !bytes.Equal(session.writes.Bytes(), []byte("show version\r")) {
		t.Fatalf("writes = %q, want %q", session.writes.Bytes(), "show version\r")
	}
}

func TestApplyTranslatesBreakOnAndOff(t *testing.T) {
	session := &recordingSession{}
	_, err := Apply(session, serial.DefaultConfig(), []Command{
		{Kind: CommandSetBreak, BoolValue: true},
		{Kind: CommandSetBreak, BoolValue: false},
	}, nil)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if len(session.breaks) != 2 {
		t.Fatalf("breaks = %+v, want two break calls", session.breaks)
	}
	if session.breaks[0] <= 0 {
		t.Fatalf("first break duration = %v, want positive duration", session.breaks[0])
	}
	if session.breaks[1] != 0 {
		t.Fatalf("second break duration = %v, want 0", session.breaks[1])
	}
}

type recordingSession struct {
	writes  bytes.Buffer
	configs []serial.Config
	dtrSet  bool
	dtr     bool
	rtsSet  bool
	rts     bool
	breaks  []time.Duration
	closed  bool
}

func (s *recordingSession) Write(data []byte) error {
	_, err := s.writes.Write(data)
	return err
}

func (s *recordingSession) SetConfig(config serial.Config) error {
	s.configs = append(s.configs, config)
	return nil
}

func (s *recordingSession) SetDTR(value bool) error {
	s.dtrSet = true
	s.dtr = value
	return nil
}

func (s *recordingSession) SetRTS(value bool) error {
	s.rtsSet = true
	s.rts = value
	return nil
}

func (s *recordingSession) SendBreak(duration time.Duration) error {
	s.breaks = append(s.breaks, duration)
	return nil
}

func (s *recordingSession) Close() error {
	s.closed = true
	return nil
}
