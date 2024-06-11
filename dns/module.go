package dns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/js/promises"
	"go.k6.io/k6/metrics"

	"github.com/grafana/sobek"
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
		common.Throw(vu.Runtime(), fmt.Errorf("failed to register dns module instance's metrics; reason: %w", err))
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
// func (mi *ModuleInstance) Resolve(query, recordType sobek.Value, resolveDNSOptions *sobek.Object) *sobek.Promise {
func (mi *ModuleInstance) Resolve(query, recordType, nameserverAddr sobek.Value) *sobek.Promise {
	promise, resolve, reject := promises.New(mi.vu)

	if mi.vu.State() == nil {
		reject(errors.New("resolve can not be used in the init context"))
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

	var nameserverAddrStr string
	if err := mi.vu.Runtime().ExportTo(nameserverAddr, &nameserverAddrStr); err != nil {
		reject(fmt.Errorf("nameserver must be a string; got %v instead", nameserverAddr))
		return promise
	}

	// nameserver := NewNameserver(options.Nameserver.IP, options.Nameserver.Port)
	nameserver, err := ParseNameserverAddr(nameserverAddrStr)
	if err != nil {
		reject(err)
		return promise
	}

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
func (mi *ModuleInstance) Lookup(hostname sobek.Value) *sobek.Promise {
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

// registerMetrics registers the metrics for the module instance.
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

// emitResolutionMetrics emits the metrics specific to DNS resolution operations.
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

// emitLookupMetrics emits the metrics specific to DNS lookup operations.
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

// moduleInstanceMetrics holds the metrics for the module instance.
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
