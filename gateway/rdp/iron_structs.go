package rdp

func ptrString(data string) *string {
	return &data
}

type RDCleanPathError struct {
	ErrorCode      uint16  `asn1:"tag:0"`
	HttpStatusCode *uint16 `asn1:"tag:1,optional"`
	WSALastError   *uint16 `asn1:"tag:2,optional"`
	TLSAlertCode   *uint16 `asn1:"tag:3,optional"`
}

func NewRDCleanPathErrorDefault() *RDCleanPathError {
	return &RDCleanPathError{
		ErrorCode: 0,
	}
}

func NewRDCleanPathError(httpError uint16) *RDCleanPathError {
	return &RDCleanPathError{
		ErrorCode:      1, // GENERAL_ERROR_CODE
		HttpStatusCode: &httpError,
	}
}

type RDCleanPathPdu struct {
	Version           uint64            `asn1:"tag:0"`
	Error             *RDCleanPathError `asn1:"tag:1,optional"`
	Destination       *string           `asn1:"tag:2,optional"`
	ProxyAuth         *string           `asn1:"tag:3,optional"`
	ServerAuth        *string           `asn1:"tag:4,optional"`
	PreconnectionBlob *string           `asn1:"tag:5,optional"`
	X224ConnectionPDU []byte            `asn1:"tag:6,optional"`
	ServerCertChain   [][]byte          `asn1:"tag:7,optional"`
	ServerAddr        *string           `asn1:"tag:9,optional"`
}

func (r *RDCleanPathPdu) Encode() ([]byte, error) {
	return marshalContextExplicit(r)
}

func (r *RDCleanPathPdu) Decode(data []byte) error {
	return unmarshalContextExplicit(data, r)
}
