//go:build windows

package discovery

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NOTE: This Windows path uses the DNS-SD API in dnsapi.dll (Windows 10+),
// which registers with the OS's own mDNS responder. It has been compile-
// checked but NOT run on Windows; validate on a real machine before relying
// on it. Advertising is best-effort (main logs a warning on failure rather
// than aborting), so a problem here degrades discovery without breaking the
// proxy.

var (
	dnsapi                     = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsServiceConstruct    = dnsapi.NewProc("DnsServiceConstructInstance")
	procDnsServiceFreeInstance = dnsapi.NewProc("DnsServiceFreeInstance")
	procDnsServiceRegister     = dnsapi.NewProc("DnsServiceRegister")
	procDnsServiceDeRegister   = dnsapi.NewProc("DnsServiceDeRegister")
)

// dnsServiceRegisterRequest mirrors DNS_SERVICE_REGISTER_REQUEST from windns.h.
type dnsServiceRegisterRequest struct {
	Version            uint32
	InterfaceIndex     uint32
	ServiceInstance    uintptr // PDNS_SERVICE_INSTANCE
	CompletionCallback uintptr
	QueryContext       uintptr
	Credentials        windows.Handle
	UnicastEnabled     int32
}

const dnsQueryRequestVersion1 = 1

// DnsServiceRegister/DeRegister return this (DNS_REQUEST_PENDING) when the
// async request has been accepted and will complete via the callback.
const dnsRequestPending = 9506

// deregisterTimeout bounds how long withdrawing the service waits for the
// responder to confirm. The instance can't be freed, nor the same name
// safely re-registered, until it does.
const deregisterTimeout = 3 * time.Second

// Completion callbacks are a limited process-wide resource
// (windows.NewCallback never frees them), so every request shares this one
// and is told apart by its QueryContext, which keys a waiting channel.
var (
	pendingMu   sync.Mutex
	pending     = map[uintptr]chan uint32{}
	nextPending uintptr
)

var registerComplete = windows.NewCallback(func(status uint32, ctx uintptr, instance uintptr) uintptr {
	pendingMu.Lock()
	ch := pending[ctx]
	delete(pending, ctx)
	pendingMu.Unlock()
	if ch != nil {
		ch <- status
	}
	return 0
})

// newPending allocates a QueryContext and the channel its completion
// status is delivered on.
func newPending() (uintptr, chan uint32) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	nextPending++
	ch := make(chan uint32, 1)
	pending[nextPending] = ch
	return nextPending, ch
}

func dropPending(ctx uintptr) {
	pendingMu.Lock()
	delete(pending, ctx)
	pendingMu.Unlock()
}

func Advertise(log *slog.Logger, port int, baseURL string, ip net.IP) (func(), error) {
	instance := instanceName()
	serviceName := instance + "." + ServiceType + ".local"

	hostName := "localhost"
	if h, err := windows.ComputerName(); err == nil && h != "" {
		hostName = shortHost(h)
	}
	hostName += ".local"

	serviceNameW, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return nil, fmt.Errorf("encode service name: %w", err)
	}
	hostNameW, err := windows.UTF16PtrFromString(hostName)
	if err != nil {
		return nil, fmt.Errorf("encode host name: %w", err)
	}

	keys, values, err := splitTXT(txtRecords(baseURL))
	if err != nil {
		return nil, err
	}

	var keysPtr, valuesPtr unsafe.Pointer
	if len(keys) > 0 {
		keysPtr = unsafe.Pointer(&keys[0])
		valuesPtr = unsafe.Pointer(&values[0])
	}

	// IP4_ADDRESS is a DWORD holding the address in network byte order, i.e.
	// the four address bytes as they sit in memory. With no address given,
	// the A record is left to the responder to fill in from the host name.
	var ip4 uint32
	var ip4Ptr unsafe.Pointer
	if v4 := ip.To4(); v4 != nil {
		ip4 = binary.LittleEndian.Uint32(v4)
		ip4Ptr = unsafe.Pointer(&ip4)
	}

	ret, _, _ := procDnsServiceConstruct.Call(
		uintptr(unsafe.Pointer(serviceNameW)),
		uintptr(unsafe.Pointer(hostNameW)),
		uintptr(ip4Ptr),
		0, // pIp6
		uintptr(uint16(port)),
		0, // priority
		0, // weight
		uintptr(len(keys)),
		uintptr(keysPtr),
		uintptr(valuesPtr),
	)
	if ret == 0 {
		return nil, fmt.Errorf("DnsServiceConstructInstance returned NULL")
	}
	instancePtr := ret

	// The request is heap-allocated and referenced by the shutdown func so it
	// stays valid for as long as the responder may use it.
	regCtx, regDone := newPending()
	req := &dnsServiceRegisterRequest{
		Version:            dnsQueryRequestVersion1,
		ServiceInstance:    instancePtr,
		CompletionCallback: registerComplete,
		QueryContext:       regCtx,
	}
	rc, _, _ := procDnsServiceRegister.Call(uintptr(unsafe.Pointer(req)), 0)
	if rc != dnsRequestPending {
		dropPending(regCtx)
		if rc != 0 {
			procDnsServiceFreeInstance.Call(instancePtr)
			return nil, fmt.Errorf("DnsServiceRegister failed: status %d", rc)
		}
	} else {
		go func() {
			if status := <-regDone; status != 0 {
				log.Warn("mdns registration failed", "instance", instance, "status", status)
			}
		}()
	}

	log.Info("mdns advertising (system responder)",
		"service", ServiceType, "instance", instance, "base_url", baseURL, "ip", ip)

	var once sync.Once
	return func() {
		once.Do(func() {
			deregCtx, deregDone := newPending()
			dereg := &dnsServiceRegisterRequest{
				Version:            dnsQueryRequestVersion1,
				ServiceInstance:    instancePtr,
				CompletionCallback: registerComplete,
				QueryContext:       deregCtx,
			}
			rc, _, _ := procDnsServiceDeRegister.Call(uintptr(unsafe.Pointer(dereg)), 0)
			if rc == dnsRequestPending {
				select {
				case <-deregDone:
				case <-time.After(deregisterTimeout):
					// Leak the instance rather than free memory the
					// responder may still touch.
					dropPending(deregCtx)
					log.Warn("mdns deregistration timed out", "instance", instance)
					runtime.KeepAlive(req)
					return
				}
			} else {
				dropPending(deregCtx)
			}
			procDnsServiceFreeInstance.Call(instancePtr)
			runtime.KeepAlive(req)
			runtime.KeepAlive(dereg)
		})
	}, nil
}

// splitTXT turns "key=value" entries into the parallel UTF-16 key/value
// pointer arrays DnsServiceConstructInstance expects.
func splitTXT(entries []string) (keys, values []*uint16, err error) {
	for _, e := range entries {
		k, v, _ := strings.Cut(e, "=")
		kw, err := windows.UTF16PtrFromString(k)
		if err != nil {
			return nil, nil, fmt.Errorf("encode txt key %q: %w", k, err)
		}
		vw, err := windows.UTF16PtrFromString(v)
		if err != nil {
			return nil, nil, fmt.Errorf("encode txt value %q: %w", v, err)
		}
		keys = append(keys, kw)
		values = append(values, vw)
	}
	return keys, values, nil
}
