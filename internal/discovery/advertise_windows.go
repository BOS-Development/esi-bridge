//go:build windows

package discovery

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
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

// DnsServiceRegister returns this (DNS_REQUEST_PENDING) when the async
// registration has been accepted and will complete via the callback.
const dnsRequestPending = 9506

// noopRegisterComplete is the required completion callback; we don't act on
// the result beyond keeping the registration alive for the process lifetime.
var noopRegisterComplete = windows.NewCallback(func(status uint32, ctx uintptr, instance uintptr) uintptr {
	return 0
})

func Advertise(log *slog.Logger, port int, baseURL string) (func(), error) {
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

	ret, _, _ := procDnsServiceConstruct.Call(
		uintptr(unsafe.Pointer(serviceNameW)),
		uintptr(unsafe.Pointer(hostNameW)),
		0, // pIp4: resolved from host name
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

	req := dnsServiceRegisterRequest{
		Version:            dnsQueryRequestVersion1,
		ServiceInstance:    instancePtr,
		CompletionCallback: noopRegisterComplete,
	}
	rc, _, _ := procDnsServiceRegister.Call(uintptr(unsafe.Pointer(&req)), 0)
	if rc != dnsRequestPending && rc != 0 {
		procDnsServiceFreeInstance.Call(instancePtr)
		return nil, fmt.Errorf("DnsServiceRegister failed: status %d", rc)
	}

	log.Info("mdns advertising (system responder)",
		"service", ServiceType, "instance", instance, "base_url", baseURL)

	var once sync.Once
	return func() {
		once.Do(func() {
			dereg := dnsServiceRegisterRequest{
				Version:            dnsQueryRequestVersion1,
				ServiceInstance:    instancePtr,
				CompletionCallback: noopRegisterComplete,
			}
			procDnsServiceDeRegister.Call(uintptr(unsafe.Pointer(&dereg)), 0)
			procDnsServiceFreeInstance.Call(instancePtr)
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
