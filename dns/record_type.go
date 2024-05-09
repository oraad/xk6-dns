package dns

// RecordType represents a DNS record type.
//
// It is a string type with a restricted set of values.
// The values are the DNS record types supported by the module.
//
// The values are:
// - A
// - AAAA
// - ANAME
// - CNAME
// - NS
// - PTR
//
// The supported values are the ones that are most likely to be
// used by the users of this extension and package, as they are
// those returning IP addresses. Other record types could be
// supported later on, as long as we extend our resolver's logic
// to support them.
//
// We use a custom type to restrict the set of values, and to
// avoid leaking the underlying dns package's types to the
// users of the reusable abstractions defined by this module.
//
//go:generate enumer -type=RecordType -trimprefix RecordType -output record_type_gen.go
type RecordType uint16

// Note that we aligned the values of the RecordType enum values with the
// corresponding values of the dns package's types for convenience.
const (
	RecordTypeA     RecordType = 1
	RecordTypeAAAA             = 28
	RecordTypeCNAME            = 5
	RecordTypeNS               = 2
	RecordTypePTR              = 12
)
