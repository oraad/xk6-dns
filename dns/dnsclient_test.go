package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/docker/go-connections/nat"

	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/testcontainers/testcontainers-go"
	"go.k6.io/k6/metrics"

	"go.k6.io/k6/lib"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"

	"go.k6.io/k6/js/compiler"
	"go.k6.io/k6/js/modulestest"
)

const (
	// defaultTestDomain is the default domain name used to configure our test DNS server, and
	// used through tests. With the testcontainer running, this domain should resolve to the
	// defaultTestIP.
	defaultTestDomain = "k6.test"

	// defaultTestRecordType is the default record type used to configure our test DNS server, and
	// used through tests. With the testcontainer running, this domain should resolve to the
	// defaultTestIP.
	defaultTestRecordType = "A"

	// defaultTestIP is the default IP address used to configure our test DNS server, and used
	// through tests. With the testcontainer running, the defaultTestDomain should resolve to this
	// IP.
	//
	// We explicitly use a "martian", non-routable IP address (as per [RFC 1918]) to avoid any potential conflicts with
	// real-world IP addresses.
	//
	// [RFC 1918]: https://datatracker.ietf.org/doc/html/rfc1918
	defaultTestIP = "203.0.113.1"
)

func TestClient_Resolve(t *testing.T) {
	t.Parallel()

	t.Run("Resolve fails in the init context", func(t *testing.T) {
		t.Parallel()

		runtime, err := newConfiguredRuntime(t)
		require.NoError(t, err)

		_, err = runtime.RunOnEventLoop(wrapInAsyncLambda(`
			await dns.resolve("k6.io", "A", { nameserver: "1.1.1.1:53" });
		`))

		// network operations are forbidden in the init context, thus
		// we explicitly expect an error here
		assert.Error(t, err)
	})

	t.Run("Resolve using common public nameserver", func(t *testing.T) {
		t.Parallel()

		runtime, err := newConfiguredRuntime(t)
		require.NoError(t, err)

		// Setting up the runtime with the necessary state
		runtime.MoveToVUContext(&lib.State{
			BuiltinMetrics: metrics.RegisterBuiltinMetrics(metrics.NewRegistry()),
			Tags:           lib.NewVUStateTags(metrics.NewRegistry().RootTagSet().With("tag-vu", "mytag")),
			Samples:        make(chan metrics.SampleContainer, 1024),
		})

		_, err = runtime.RunOnEventLoop(wrapInAsyncLambda(`
			await dns.resolve("k6.io", "A", { nameserver: "1.1.1.1:53" });
		`))

		assert.NoError(t, err)
	})

	t.Run("Resolve against local unbound dns server and custom domain", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		unboundContainer, mappedPort := startUnboundContainer(t, ctx)
		defer func() {
			if err := unboundContainer.Terminate(ctx); err != nil { //nolint:govet
				t.Fatalf("Could not stop unbound: %s", err)
			}
		}()

		runtime, err := newConfiguredRuntime(t)
		require.NoError(t, err)

		// Setting up the runtime with the necessary state
		runtime.MoveToVUContext(&lib.State{
			BuiltinMetrics: metrics.RegisterBuiltinMetrics(metrics.NewRegistry()),
			Tags:           lib.NewVUStateTags(metrics.NewRegistry().RootTagSet().With("tag-vu", "mytag")),
			Samples:        make(chan metrics.SampleContainer, 1024),
		})

		// FIXME: we should get the IP programatically
		_, err = runtime.RunOnEventLoop(wrapInAsyncLambda(fmt.Sprintf(`
			const resolveResults = await dns.resolve(%q, %q, { nameserver: "127.0.0.1:%d" });

			if (resolveResults.length === 0) {
				throw "Resolving k6.local against unbound server test container returned no results, expected '%s'"
			}
			
			if (resolveResults.length !== 1) {
				throw "Resolving k6.local against unbound server test container returned too many results, expected ['%s'], got " + resolveResults
			}
			
			if (resolveResults[0] !== %q) {
				throw "Resolving k6.local against unbound server test container returned unexpected result, expected '%s', got " + resolveResults[0]
			}
		`, defaultTestDomain, defaultTestRecordType, mappedPort.Int(), defaultTestIP, defaultTestIP, defaultTestIP, defaultTestIP)))

		assert.NoError(t, err)
	})

	t.Run("Lookup fails in the init context", func(t *testing.T) {
		t.Parallel()

		runtime, err := newConfiguredRuntime(t)
		require.NoError(t, err)

		_, err = runtime.RunOnEventLoop(wrapInAsyncLambda(`
			await dns.lookup("k6.io");
		`))

		// network operations are forbidden in the init context, thus
		// we explicitly expect an error here
		assert.Error(t, err)
	})

	t.Run("Lookup returns the system's default resolver results", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		wantIPs, err := net.DefaultResolver.LookupHost(ctx, "k6.io")
		require.NoError(t, err)

		runtime, err := newConfiguredRuntime(t)
		require.NoError(t, err)

		// Setting up the runtime with the necessary state
		runtime.MoveToVUContext(&lib.State{
			BuiltinMetrics: metrics.RegisterBuiltinMetrics(metrics.NewRegistry()),
			Tags:           lib.NewVUStateTags(metrics.NewRegistry().RootTagSet().With("tag-vu", "mytag")),
			Samples:        make(chan metrics.SampleContainer, 1024),
		})

		// TODO: we should verify that the lookupResults are the same as wantIPs
		_, gotErr := runtime.RunOnEventLoop(wrapInAsyncLambda(fmt.Sprintf(`
			const lookupResults = await dns.lookup("k6.io");

			if (lookupResults.length === 0) {
				throw "Looking up k6.io using the system's default resolver returned no results, which is unexpected"
			}

			if (lookupResults.length !== %d) {
				throw "Looking up k6.io using the system's default resolver returned unexpected number of results, expected %d, got " + lookupResults
			}
		`, len(wantIPs), len(wantIPs))))

		assert.NoError(t, gotErr)
	})
}

const initGlobals = `
	globalThis.dns = require("k6/x/dns");
`

func newConfiguredRuntime(t testing.TB) (*modulestest.Runtime, error) {
	runtime := modulestest.NewRuntime(t)

	err := runtime.SetupModuleSystem(map[string]interface{}{"k6/x/dns": New()}, nil, compiler.New(runtime.VU.InitEnv().Logger))
	if err != nil {
		return nil, err
	}

	// Ensure the `fs` module is available in the VU's runtime.
	_, err = runtime.VU.Runtime().RunString(initGlobals)

	return runtime, err
}

// wrapInAsyncLambda is a helper function that wraps the provided input in an async lambda. This
// makes the use of `await` statements in the input possible.
func wrapInAsyncLambda(input string) string {
	// This makes it possible to use `await` freely on the "top" level
	return "(async () => {\n " + input + "\n })()"
}

func startUnboundContainer(
	t *testing.T,
	ctx context.Context,
) (runningContainer testcontainers.Container, mappedPort nat.Port) {
	// We configure unbound to point the k6.local domain to 192.168.42.42
	domainConfiguration := fmt.Sprintf(
		`local-data: "%s. %s %s"`,
		defaultTestDomain, defaultTestRecordType, defaultTestIP,
	)

	req := testcontainers.ContainerRequest{
		Image: "mvance/unbound:latest",
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(domainConfiguration),
				ContainerFilePath: "/opt/unbound/etc/unbound/a-records.conf",
			},
		},
		ExposedPorts: []string{"53/tcp", "53/udp"},
		WaitingFor:   wait.ForListeningPort("53/udp"),
	}

	runningContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatal(err)
	}

	mappedPort, err = runningContainer.MappedPort(ctx, "53/udp")
	require.NoError(t, err)

	return runningContainer, mappedPort
}
