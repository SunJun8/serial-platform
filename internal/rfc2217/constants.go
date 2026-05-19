package rfc2217

const (
	SE   byte = 240
	SB   byte = 250
	WILL byte = 251
	WONT byte = 252
	DO   byte = 253
	DONT byte = 254
	IAC  byte = 255
)

const (
	COMPortOption byte = 44
)

const (
	SetBaudrate byte = 1
	SetDataSize byte = 2
	SetParity   byte = 3
	SetStopSize byte = 4
	SetControl  byte = 5
)

const (
	ControlBreakStateRequest byte = 4
	ControlBreakStateOn      byte = 5
	ControlBreakStateOff     byte = 6
	ControlDTRStateRequest   byte = 7
	ControlDTRStateOn        byte = 8
	ControlDTRStateOff       byte = 9
	ControlRTSStateRequest   byte = 10
	ControlRTSStateOn        byte = 11
	ControlRTSStateOff       byte = 12
)

const (
	ControlBreakON  = ControlBreakStateOn
	ControlBreakOFF = ControlBreakStateOff
	ControlDTRON    = ControlDTRStateOn
	ControlDTROFF   = ControlDTRStateOff
	ControlRTSON    = ControlRTSStateOn
	ControlRTSOFF   = ControlRTSStateOff
)
