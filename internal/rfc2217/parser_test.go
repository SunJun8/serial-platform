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

func TestParserHandlesSplitSubnegotiation(t *testing.T) {
	parser := NewParser()

	ops, err := parser.Feed([]byte{IAC, SB, COMPortOption, SetBaudrate, 0x00})
	if err != nil {
		t.Fatalf("Feed first chunk returned error: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops after first chunk = %+v, want empty", ops)
	}

	ops, err = parser.Feed([]byte{0x00, 0xe1, 0x00, IAC, SE})
	if err != nil {
		t.Fatalf("Feed second chunk returned error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	if ops[0].Kind != OperationCommand {
		t.Fatalf("operation kind = %v, want command", ops[0].Kind)
	}
	if ops[0].Command.Kind != CommandSetBaudrate || ops[0].Command.Baudrate != 57600 {
		t.Fatalf("command = %+v, want baud 57600", ops[0].Command)
	}
}

func TestParserHandlesSplitEscapedIACData(t *testing.T) {
	parser := NewParser()

	ops, err := parser.Feed([]byte{'a', IAC})
	if err != nil {
		t.Fatalf("Feed first chunk returned error: %v", err)
	}
	got := dataFromOperations(ops)

	ops, err = parser.Feed([]byte{IAC, 'b'})
	if err != nil {
		t.Fatalf("Feed second chunk returned error: %v", err)
	}
	got = append(got, dataFromOperations(ops)...)

	want := []byte{'a', IAC, 'b'}
	if !bytes.Equal(got, want) {
		t.Fatalf("data = %x, want %x", got, want)
	}
}

func TestParserNegotiatesCOMPortOption(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{
			name: "will comport option",
			in:   []byte{IAC, WILL, COMPortOption},
			want: []byte{IAC, DO, COMPortOption},
		},
		{
			name: "do comport option",
			in:   []byte{IAC, DO, COMPortOption},
			want: []byte{IAC, WILL, COMPortOption},
		},
		{
			name: "will unsupported option",
			in:   []byte{IAC, WILL, 1},
			want: []byte{IAC, DONT, 1},
		},
		{
			name: "do unsupported option",
			in:   []byte{IAC, DO, 1},
			want: []byte{IAC, WONT, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser()
			ops, err := parser.Feed(tt.in)
			if err != nil {
				t.Fatalf("Feed returned error: %v", err)
			}
			if len(ops) != 1 {
				t.Fatalf("len(ops) = %d, want 1", len(ops))
			}
			if ops[0].Kind != OperationReply {
				t.Fatalf("operation kind = %v, want reply", ops[0].Kind)
			}
			if !bytes.Equal(ops[0].Data, tt.want) {
				t.Fatalf("reply = %x, want %x", ops[0].Data, tt.want)
			}
		})
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

func TestApplyOperationsPreservesStreamOrder(t *testing.T) {
	session := &recordingSession{}
	config := serial.DefaultConfig()
	ops := []Operation{
		{Kind: OperationData, Data: []byte("first")},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetBaudrate, Baudrate: 9600}},
		{Kind: OperationData, Data: []byte("second")},
	}

	got, _, err := ApplyOperations(session, config, ops)
	if err != nil {
		t.Fatalf("ApplyOperations returned error: %v", err)
	}
	if got.Baud != 9600 {
		t.Fatalf("baud = %d, want 9600", got.Baud)
	}
	wantCalls := []string{"write:first", "config:9600", "write:second"}
	if !equalStrings(session.calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", session.calls, wantCalls)
	}
}

func TestApplyOperationsQueriesReturnCurrentConfigWithoutSetConfig(t *testing.T) {
	session := &recordingSession{}
	config := serial.Config{Baud: 115200, DataBits: 8, Parity: "N", StopBits: 1, Flow: "none"}
	ops := []Operation{
		{Kind: OperationCommand, Command: Command{Kind: CommandSetBaudrate, Query: true}},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetDataBits, Query: true}},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetParity, Query: true}},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetStopBits, Query: true}},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetControl, Query: true}},
	}

	got, response, err := ApplyOperations(session, config, ops)
	if err != nil {
		t.Fatalf("ApplyOperations returned error: %v", err)
	}
	if got != config {
		t.Fatalf("config = %+v, want unchanged %+v", got, config)
	}
	if len(session.configs) != 0 {
		t.Fatalf("configs = %+v, want none", session.configs)
	}
	want := append([]byte{},
		serverSubnegotiation(SetBaudrate+100, []byte{0x00, 0x01, 0xc2, 0x00})...)
	want = append(want, serverSubnegotiation(SetDataSize+100, []byte{8})...)
	want = append(want, serverSubnegotiation(SetParity+100, []byte{1})...)
	want = append(want, serverSubnegotiation(SetStopSize+100, []byte{1})...)
	want = append(want, serverSubnegotiation(SetControl+100, []byte{0})...)
	if !bytes.Equal(response, want) {
		t.Fatalf("response = %x, want %x", response, want)
	}
}

func TestApplyOperationsConfirmsAppliedSetCommands(t *testing.T) {
	session := &recordingSession{}
	config := serial.DefaultConfig()
	ops := []Operation{
		{Kind: OperationCommand, Command: Command{Kind: CommandSetDataBits, IntValue: 7}},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetParity, IntValue: 3}},
		{Kind: OperationCommand, Command: Command{Kind: CommandSetStopBits, IntValue: 2}},
	}

	got, response, err := ApplyOperations(session, config, ops)
	if err != nil {
		t.Fatalf("ApplyOperations returned error: %v", err)
	}
	if got.DataBits != 7 || got.Parity != "E" || got.StopBits != 2 {
		t.Fatalf("config = %+v, want data 7 parity E stop 2", got)
	}
	want := append([]byte{}, serverSubnegotiation(SetDataSize+100, []byte{7})...)
	want = append(want, serverSubnegotiation(SetParity+100, []byte{3})...)
	want = append(want, serverSubnegotiation(SetStopSize+100, []byte{2})...)
	if !bytes.Equal(response, want) {
		t.Fatalf("response = %x, want %x", response, want)
	}
}

func TestApplyOperationsRejectsUnsupportedOnePointFiveStopBits(t *testing.T) {
	session := &recordingSession{}
	_, _, err := ApplyOperations(session, serial.DefaultConfig(), []Operation{
		{Kind: OperationCommand, Command: Command{Kind: CommandSetStopBits, IntValue: 3}},
	})
	if err == nil {
		t.Fatal("ApplyOperations returned nil error, want unsupported stop bits error")
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
	calls   []string
}

func (s *recordingSession) Write(data []byte) error {
	s.calls = append(s.calls, "write:"+string(data))
	_, err := s.writes.Write(data)
	return err
}

func (s *recordingSession) SetConfig(config serial.Config) error {
	s.configs = append(s.configs, config)
	s.calls = append(s.calls, "config:"+itoa(config.Baud))
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

func dataFromOperations(ops []Operation) []byte {
	var out []byte
	for _, op := range ops {
		if op.Kind == OperationData {
			out = append(out, op.Data...)
		}
	}
	return out
}

func serverSubnegotiation(command byte, value []byte) []byte {
	out := []byte{IAC, SB, COMPortOption, command}
	out = append(out, value...)
	out = append(out, IAC, SE)
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
