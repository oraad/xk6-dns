package dns

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/miekg/dns"
)

// Resolver is the interface that wraps the Resolve method.
//
// Resolve resolves a domain name to an IP address. It returns a slice of IP
// addresses as strings.
type Resolver interface {
	Resolve(ctx context.Context, query, recordType string, nameserver Nameserver) ([]string, error)
}

// Lookuper is the interface that wraps the Lookup method.
//
// As opposed to a Resolver which uses a specific nameserver to resolve the
// query, a Lookuper uses the system's default resolver.
//
// Lookup resolves a domain name to an IP address. It returns a slice of IP
// addresses as strings.
type Lookuper interface {
	Lookup(ctx context.Context, hostname string) ([]string, error)
}

// Client is a DNS resolver that uses the `miekg/dns` package under the hood.
//
// It implements the Resolver interface.
type Client struct {
	// client is the DNS client used to resolve queries.
	client dns.Client
}

// Ensure our Client implements the Resolver interface
var _ Resolver = &Client{}

// Ensure our Client implements the Lookuper interface
var _ Lookuper = &Client{}

// NewDNSClient creates a new Client.
func NewDNSClient() *Client {
	return &Client{
		client: dns.Client{},
	}
}

// Resolve resolves a domain name to a slice of IP addresses using the given nameserver.
// It returns a slice of IP addresses as strings.
func (r *Client) Resolve(
	ctx context.Context,
	query, recordType string,
	nameserver Nameserver,
) ([]string, error) {
	rt, err := RecordTypeString(recordType)
	if err != nil {
		return nil, fmt.Errorf("invalid record type: %w", err)
	}

	// Because the dns package [dns.SetQuestion] function expects specific
	// uint16 values for the record type, and we don't want to leak that
	// to our public API, we need to convert our RecordType to the
	// corresponding uint16 value.
	// TODO: could we get the enumer generator to do that instead?
	var concreteType uint16
	switch rt {
	case RecordTypeA:
		concreteType = dns.TypeA
	case RecordTypeAAAA:
		concreteType = dns.TypeAAAA
	}

	// Prepare the DNS query message
	message := dns.Msg{}
	message.SetQuestion(query+".", concreteType)

	// Query the nameserver
	response, _, err := r.client.ExchangeContext(ctx, &message, nameserver.Addr())
	if err != nil {
		return nil, err
	}

	var ips []string
	for _, a := range response.Answer {
		switch t := a.(type) {
		case *dns.A:
			ips = append(ips, t.A.String())
		case *dns.AAAA:
			ips = append(ips, t.AAAA.String())
		case *dns.CNAME:
			ips = append(ips, t.Target)
		case *dns.NS:
			ips = append(ips, t.Ns)
		case *dns.PTR:
			ips = append(ips, t.Ptr)
		default:
			return nil, fmt.Errorf("unhandled DNS answer type %T", a)
		}
	}

	return ips, nil
}

// Lookup resolves a domain name to a slice of IP addresses using the system's
// default resolver.
func (r *Client) Lookup(ctx context.Context, hostname string) ([]string, error) {
	ips, err := net.DefaultResolver.LookupHost(ctx, hostname)
	if err != nil {
		return nil, err
	}

	return ips, nil
}

// Nameserver represents a DNS nameserver.
type Nameserver struct {
	// IPAddr is the IP address of the nameserver.
	IP net.IP

	// Port is the port of the nameserver.
	Port uint16
}

// Addr returns the address of the nameserver as a string.
func (n Nameserver) Addr() string {
	return n.IP.String() + ":" + strconv.Itoa(int(n.Port))
}

// NewNameserver creates a new Nameserver with the given IP address and port.
func NewNameserver(ip net.IP, port uint16) Nameserver {
	return Nameserver{
		IP:   ip,
		Port: port,
	}
}
