package mssqltypes

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"
)

// OptionFlags3
// http://msdn.microsoft.com/en-us/library/dd304019.aspx
const (
	fChangePassword byte = 1 << iota
	fSendYukonBinaryXML
	fUserInstance
	fUnknownCollationHandling
	fExtension
)

// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/773a62b6-ee89-4c02-9e5e-344882630aac
type login struct {
	HostName       string
	UserName       string
	Password       string
	AppName        string
	ServerName     string
	CtlIntName     string
	Language       string
	Database       string
	ClientID       [6]byte
	SSPI           []byte
	AtchDBFile     string
	ChangePassword string
	FeatureExt     []byte

	header *loginHeader
}

type loginHeader struct {
	Length               uint32
	TDSVersion           uint32
	PacketSize           uint32
	ClientProgVer        uint32
	ClientPID            uint32
	ConnectionID         uint32
	OptionFlags1         byte
	OptionFlags2         byte
	TypeFlags            byte
	OptionFlags3         byte
	ClientTimeZone       int32 // This field is not used and can be set to zero.
	ClientLCID           uint32
	HostNameOffset       uint16
	HostNameLength       uint16
	UserNameOffset       uint16
	UserNameLength       uint16
	PasswordOffset       uint16
	PasswordLength       uint16
	AppNameOffset        uint16
	AppNameLength        uint16
	ServerNameOffset     uint16
	ServerNameLength     uint16
	ExtensionOffset      uint16
	ExtensionLength      uint16
	CtlIntNameOffset     uint16
	CtlIntNameLength     uint16
	LanguageOffset       uint16
	LanguageLength       uint16
	DatabaseOffset       uint16
	DatabaseLength       uint16
	ClientID             [6]byte
	SSPIOffset           uint16
	SSPILength           uint16
	AtchDBFileOffset     uint16
	AtchDBFileLength     uint16
	ChangePasswordOffset uint16
	ChangePasswordLength uint16
	SSPILongLength       uint32
}

func (l *login) PacketSize() uint32 { return l.header.PacketSize }
func (l *login) TDSVersion() uint32 { return l.header.TDSVersion }

// DisablePasswordChange change the OptionFlag3 to disable processing password change
func (l *login) DisablePasswordChange() {
	if l.header.OptionFlags3&fChangePassword > 0 {
		l.header.OptionFlags3 &= ^fChangePassword
	}
}

func DecodeLogin(data []byte) *login {
	buf := bytes.NewBuffer(data)
	l := &login{header: &loginHeader{}}
	l.header.Length = binary.LittleEndian.Uint32(buf.Next(4))
	l.header.TDSVersion = binary.LittleEndian.Uint32(buf.Next(4))
	l.header.PacketSize = binary.LittleEndian.Uint32(buf.Next(4))
	l.header.ClientProgVer = binary.LittleEndian.Uint32(buf.Next(4))
	l.header.ClientPID = binary.LittleEndian.Uint32(buf.Next(4))
	l.header.ConnectionID = binary.LittleEndian.Uint32(buf.Next(4))

	flags := buf.Next(4)
	l.header.OptionFlags1 = flags[0]
	l.header.OptionFlags2 = flags[1]
	l.header.TypeFlags = flags[2]
	l.header.OptionFlags3 = flags[3]

	// This field is not used and can be set to zero.
	// client timezone
	_ = buf.Next(4)

	l.header.ClientLCID = binary.LittleEndian.Uint32(buf.Next(4))

	l.header.HostNameOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.HostNameLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.UserNameOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.UserNameLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.PasswordOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.PasswordLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.AppNameOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.AppNameLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.ServerNameOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.ServerNameLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.ExtensionOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.ExtensionLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.CtlIntNameOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.CtlIntNameLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.LanguageOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.LanguageLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.DatabaseOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.DatabaseLength = binary.LittleEndian.Uint16(buf.Next(2))

	var clientID [6]byte
	copy(clientID[:], buf.Next(6))
	l.header.ClientID = clientID // not used
	l.header.SSPIOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.SSPILength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.AtchDBFileOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.AtchDBFileLength = binary.LittleEndian.Uint16(buf.Next(2))

	l.header.ChangePasswordOffset = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.ChangePasswordLength = binary.LittleEndian.Uint16(buf.Next(2))
	l.header.SSPILongLength = binary.LittleEndian.Uint32(buf.Next(4))

	l.HostName = getOption(data, l.header.HostNameOffset, l.header.HostNameLength)
	l.UserName = getOption(data, l.header.UserNameOffset, l.header.UserNameLength)
	// the password it's not used neither decoded properly (unmangled)
	l.Password = getOption(data, l.header.PasswordOffset, l.header.PasswordLength)
	l.AppName = getOption(data, l.header.AppNameOffset, l.header.AppNameLength)
	l.ServerName = getOption(data, l.header.ServerNameOffset, l.header.ServerNameLength)
	// l.FeatureExt = []byte(getOption(data, l.header.ExtensionOffset, l.header.ExtensionLength))
	l.CtlIntName = getOption(data, l.header.CtlIntNameOffset, l.header.CtlIntNameLength)
	l.Language = getOption(data, l.header.LanguageOffset, l.header.LanguageLength)
	l.Database = getOption(data, l.header.DatabaseOffset, l.header.DatabaseLength)
	// TODO: fix this?
	// l.SSPI = []byte(getOption(data, l.header.SSPIOffset, l.header.SSPILength))
	l.AtchDBFile = getOption(data, l.header.AtchDBFileOffset, l.header.AtchDBFileLength)
	l.ChangePassword = getOption(data, l.header.ChangePasswordOffset, l.header.ChangePasswordLength)

	if l.header.ExtensionOffset > 0 {
		featureExtOffset := binary.LittleEndian.Uint32(
			data[l.header.ExtensionOffset : l.header.ExtensionOffset+l.header.ExtensionLength])
		l.FeatureExt = data[featureExtOffset:]
	}

	return l
}

func EncodeLogin(l login) (*Packet, error) {
	hostname := str2ucs2(l.HostName)
	username := str2ucs2(l.UserName)
	password := manglePassword(l.Password)
	appname := str2ucs2(l.AppName)
	servername := str2ucs2(l.ServerName)
	ctlintname := str2ucs2(l.CtlIntName)
	language := str2ucs2(l.Language)
	database := str2ucs2(l.Database)
	atchdbfile := str2ucs2(l.AtchDBFile)
	changepassword := str2ucs2(l.ChangePassword)
	hdr := &loginHeader{
		TDSVersion:           l.header.TDSVersion,
		PacketSize:           l.header.PacketSize,
		ClientProgVer:        l.header.ClientProgVer,
		ClientPID:            l.header.ClientPID,
		ConnectionID:         l.header.ConnectionID,
		OptionFlags1:         l.header.OptionFlags1,
		OptionFlags2:         l.header.OptionFlags2,
		TypeFlags:            l.header.TypeFlags,
		OptionFlags3:         l.header.OptionFlags3,
		ClientTimeZone:       l.header.ClientTimeZone, // This field is not used and can be set to zero.
		ClientLCID:           l.header.ClientLCID,
		HostNameLength:       uint16(utf8.RuneCountInString(l.HostName)),
		UserNameLength:       uint16(utf8.RuneCountInString(l.UserName)),
		PasswordLength:       uint16(utf8.RuneCountInString(l.Password)),
		AppNameLength:        uint16(utf8.RuneCountInString(l.AppName)),
		ServerNameLength:     uint16(utf8.RuneCountInString(l.ServerName)),
		CtlIntNameLength:     uint16(utf8.RuneCountInString(l.CtlIntName)),
		LanguageLength:       uint16(utf8.RuneCountInString(l.Language)),
		DatabaseLength:       uint16(utf8.RuneCountInString(l.Database)),
		ClientID:             l.ClientID,
		SSPILength:           uint16(len(l.SSPI)),
		AtchDBFileLength:     uint16(utf8.RuneCountInString(l.AtchDBFile)),
		ChangePasswordLength: uint16(utf8.RuneCountInString(l.ChangePassword)),
	}
	offset := uint16(binary.Size(hdr))
	hdr.HostNameOffset = offset
	offset += uint16(len(hostname))
	hdr.UserNameOffset = offset
	offset += uint16(len(username))
	hdr.PasswordOffset = offset
	offset += uint16(len(password))
	hdr.AppNameOffset = offset
	offset += uint16(len(appname))
	hdr.ServerNameOffset = offset
	offset += uint16(len(servername))
	hdr.CtlIntNameOffset = offset
	offset += uint16(len(ctlintname))
	hdr.LanguageOffset = offset
	offset += uint16(len(language))
	hdr.DatabaseOffset = offset
	offset += uint16(len(database))
	hdr.SSPIOffset = offset
	offset += uint16(len(l.SSPI))
	hdr.AtchDBFileOffset = offset
	offset += uint16(len(atchdbfile))
	hdr.ChangePasswordOffset = offset
	offset += uint16(len(changepassword))

	featureExtOffset := uint32(0)
	featureExtLen := len(l.FeatureExt)
	if featureExtLen > 0 {
		hdr.OptionFlags3 |= fExtension
		hdr.ExtensionOffset = offset
		hdr.ExtensionLength = 4
		offset += hdr.ExtensionLength // DWORD
		featureExtOffset = uint32(offset)
	}
	hdr.Length = uint32(offset) + uint32(featureExtLen)

	w := bytes.NewBuffer([]byte{})
	err := binary.Write(w, binary.LittleEndian, hdr)
	if err != nil {
		return nil, err
	}
	for _, data := range [][]byte{
		hostname, username, password, appname, servername, ctlintname,
		language, database, l.SSPI, atchdbfile, changepassword,
	} {
		_, err = w.Write(data)
		if err != nil {
			return nil, err
		}
	}

	if featureExtLen > 0 {
		err = binary.Write(w, binary.LittleEndian, featureExtOffset)
		if err != nil {
			return nil, err
		}
		_, err = w.Write(l.FeatureExt)
		if err != nil {
			return nil, err
		}
	}

	return New(PacketLogin7Type, w.Bytes()), nil
}

func getOption(v []byte, offset, length uint16) string {
	if offset == 0 || length == 0 {
		return ""
	}
	return ucs22str(v[offset : offset+length*2])
}

func ucs22str(s []byte) string {
	buf := make([]uint16, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		buf[i/2] = binary.LittleEndian.Uint16(s[i:])
	}
	return string(utf16.Decode(buf))
}

// convert Go string to UTF-16 encoded []byte (littleEndian)
// done manually rather than using bytes and binary packages
// for performance reasons
func str2ucs2(s string) []byte {
	res := utf16.Encode([]rune(s))
	ucs2 := make([]byte, 2*len(res))
	for i := 0; i < len(res); i++ {
		ucs2[2*i] = byte(res[i])
		ucs2[2*i+1] = byte(res[i] >> 8)
	}
	return ucs2
}

func manglePassword(password string) []byte {
	var ucs2password []byte = str2ucs2(password)
	for i, ch := range ucs2password {
		ucs2password[i] = ((ch<<4)&0xff | (ch >> 4)) ^ 0xA5
	}
	return ucs2password
}
