package dns

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/js/promises"
	"go.k6.io/k6/metrics"
)

type (
	// RootModule is the module that will be registered with the runtime.
	RootModule struct{}

	// ModuleInstance is the module instance that will be created for each VU.
	ModuleInstance struct {
		vu        modules.VU
		dnsClient *Client
		metrics   *moduleInstanceMetrics
	}
)

// Ensure the interfaces are implemented correctly
var (
	_ modules.Instance = &ModuleInstance{}
	_ modules.Module   = &RootModule{}
)

// New creates a new RootModule instance.
func New() *RootModule {
	return &RootModule{}
}

// NewModuleInstance creates a new instance of the module for a specific VU.
func (rm *RootModule) NewModuleInstance(vu modules.VU) modules.Instance {
	instanceMetrics, err := registerMetrics(metrics.NewRegistry())
	if err != nil {
		common.Throw(vu.Runtime(), fmt.Errorf("failed to register instanceMetrics; reason: %w", err))
	}

	return &ModuleInstance{
		vu:        vu,
		dnsClient: NewDNSClient(),
		metrics:   instanceMetrics,
	}
}

// Exports returns the module exports, that will be available in the runtime.
func (mi *ModuleInstance) Exports() modules.Exports {
	return modules.Exports{Named: map[string]interface{}{
		"resolve": mi.Resolve,
		"lookup":  mi.Lookup,
	}}
}

// Resolve resolves a domain name to an IP address.
func (mi *ModuleInstance) Resolve(query, recordType goja.Value, resolveDNSOptions *goja.Object) *goja.Promise {
	promise, resolve, reject := promises.New(mi.vu)

	if mi.vu.State() == nil {
		reject(fmt.Errorf("resolve can not be used in the init context"))
		return promise
	}

	var queryStr string
	if err := mi.vu.Runtime().ExportTo(query, &queryStr); err != nil {
		reject(fmt.Errorf("query must be a string; got %v instead", query))
		return promise
	}

	var recordTypeStr string
	if err := mi.vu.Runtime().ExportTo(recordType, &recordTypeStr); err != nil {
		reject(fmt.Errorf("recordType must be a string; got %v instead", recordType))
		return promise
	}

	options, err := newResolveOptionsFrom(mi.vu.Runtime(), resolveDNSOptions)
	if err != nil {
		reject(err)
		return promise
	}

	// nameserver := NewNameserver(options.Nameserver.IP, options.Nameserver.Port)
	nameserver, err := options.ParseNameserver()
	if err != nil {
		reject(err)
		return promise
	}

	// FIXME: do we want to support no namerservers provided, and use the default system lookup instead?
	go func() {
		resolutionStartTime := time.Now()
		fetchedIPs, resolveErr := mi.dnsClient.Resolve(mi.vu.Context(), queryStr, recordTypeStr, nameserver)
		if resolveErr != nil {
			reject(resolveErr)
			return
		}
		sinceResolutionStart := time.Since(resolutionStartTime).Milliseconds()

		// Emit the metrics
		mi.emitResolutionMetrics(
			mi.vu.Context(),
			sinceResolutionStart,
			queryStr,
			recordTypeStr,
			nameserver,
		)

		resolve(fetchedIPs)
	}()

	return promise
}

// Lookup resolves a domain name to an IP address using the default system nameservers.
func (mi *ModuleInstance) Lookup(hostname goja.Value) *goja.Promise {
	promise, resolve, reject := promises.New(mi.vu)

	if mi.vu.State() == nil {
		reject(fmt.Errorf("lookup can not be used in the init context"))
		return promise
	}

	var hostnameStr string
	if err := mi.vu.Runtime().ExportTo(hostname, &hostnameStr); err != nil {
		reject(fmt.Errorf("hostname must be a string; got %v instead", hostname))
		return promise
	}

	go func() {
		lookupStartTime := time.Now()
		ips, err := mi.dnsClient.Lookup(mi.vu.Context(), hostnameStr)
		if err != nil {
			reject(err)
			return
		}
		sinceLookupStart := time.Since(lookupStartTime).Milliseconds()

		// Emit the metrics
		mi.emitLookupMetrics(
			mi.vu.Context(),
			sinceLookupStart,
			hostnameStr,
		)

		resolve(ips)
	}()

	return promise
}

// ResolveOptions holds the options for the resolve function.
type resolveOptions struct {
	// Nameservers holds the list of DNS servers to use
	Nameserver string `js:"nameserver"`
}

// newResolveOptionsFrom creates a new ResolveOptions from a JS object.
func newResolveOptionsFrom(rt *goja.Runtime, obj *goja.Object) (*resolveOptions, error) {
	options := resolveOptions{}

	if err := rt.ExportTo(obj, &options); err != nil {
		return nil, fmt.Errorf("options must be a ResolveOptions object")
	}

	return &options, nil
}

func (ro *resolveOptions) ParseNameserver() (Nameserver, error) {
	var hostStr string
	portStr := "53"
	var err error

	// If a port was explicitly provided, let's extract and use it.
	if strings.ContainsRune(ro.Nameserver, ':') {
		// Split the host and the port from the nameserver string
		hostStr, portStr, err = net.SplitHostPort(ro.Nameserver)
		if err != nil {
			return Nameserver{}, fmt.Errorf("invalid nameserver address: %w", err)
		}
	} else {
		// Otherwise treat the options nameserver string as the host and use the default DNS port 53.
		hostStr = ro.Nameserver
	}

	// Convert the port to an integer
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Nameserver{}, fmt.Errorf("invalid port: %w", err)
	}

	return NewNameserver(net.ParseIP(hostStr), uint16(port)), nil
}

// emitResolutionMetrics emits the metrics for a websocket connection.
func (mi *ModuleInstance) emitResolutionMetrics(
	ctx context.Context,
	duration int64,
	query,
	recordType string,
	nameserver Nameserver,
) {
	state := mi.vu.State()

	tags := state.Tags.GetCurrentValues().Tags
	tags = tags.With("query", query)
	tags = tags.With("recordType", recordType)
	tags = tags.With("nameserver", nameserver.Addr())

	now := time.Now()

	// Increment the DNS lookups counter
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{
			Metric: mi.metrics.DNSResolutions,
			Tags:   tags,
		},
		Time:     now,
		Metadata: nil,
		Value:    float64(1),
	})

	// Emit the DNS lookup duration
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{
			Metric: mi.metrics.DNSResolutionDuration,
			Tags:   tags,
		},
		Time:     now,
		Value:    float64(duration),
		Metadata: nil,
	})
}

// emitLookupMetrics emits the metrics for a websocket connection.
func (mi *ModuleInstance) emitLookupMetrics(
	ctx context.Context,
	duration int64,
	host string,
) {
	state := mi.vu.State()

	tags := state.Tags.GetCurrentValues().Tags
	tags = tags.With("host", host)

	now := time.Now()

	// Increment the DNS lookups counter
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{
			Metric: mi.metrics.DNSLookups,
			Tags:   tags,
		},
		Time:     now,
		Metadata: nil,
		Value:    float64(1),
	})

	// Emit the DNS lookup duration
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{
			Metric: mi.metrics.DNSLookupDuration,
			Tags:   tags,
		},
		Time:     now,
		Value:    float64(duration),
		Metadata: nil,
	})
}

type moduleInstanceMetrics struct {
	// DNSResolutions is a counter metric that counts the number of DNS resolutions.
	DNSResolutions *metrics.Metric

	// DNSResolutionDuration is a trend metric that measures the duration of DNS resolutions.
	DNSResolutionDuration *metrics.Metric

	// DNSLookups is a counter metric that counts the number of DNS lookups.
	DNSLookups *metrics.Metric

	// DNSLookupDuration is a trend metric that measures the duration of DNS lookups.
	DNSLookupDuration *metrics.Metric
}

func registerMetrics(registry *metrics.Registry) (*moduleInstanceMetrics, error) {
	var err error
	m := &moduleInstanceMetrics{}

	m.DNSResolutions, err = registry.NewMetric("dns_resolutions", metrics.Counter)
	if err != nil {
		return nil, err
	}

	m.DNSResolutionDuration, err = registry.NewMetric("dns_resolution_duration", metrics.Trend, metrics.Time)
	if err != nil {
		return nil, err
	}

	m.DNSLookups, err = registry.NewMetric("dns_lookups", metrics.Counter)
	if err != nil {
		return nil, err
	}

	m.DNSLookupDuration, err = registry.NewMetric("dns_lookup_duration", metrics.Trend, metrics.Time)
	if err != nil {
		return nil, err
	}

	return m, nil
}
