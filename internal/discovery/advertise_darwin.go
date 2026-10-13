//go:build darwin

package discovery

/*
#include <dns_sd.h>
#include <arpa/inet.h>
#include <stdlib.h>

// htons is a macro on macOS, which cgo can't reference directly, so wrap it.
static uint16_t esi_htons(uint16_t v) { return htons(v); }
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"unsafe"
)

// Advertise registers the service with the macOS system responder
// (mDNSResponder) through the Bonjour C API. The registration stays live for
// as long as the returned DNSServiceRef connection is open, so the shutdown
// func deallocates it to withdraw the service.
func Advertise(log *slog.Logger, port int, baseURL string) (func(), error) {
	instance := instanceName()

	cName := C.CString(instance)
	defer C.free(unsafe.Pointer(cName))
	cType := C.CString(ServiceType)
	defer C.free(unsafe.Pointer(cType))

	txt := packTXT(txtRecords(baseURL))

	var ref C.DNSServiceRef
	rc := C.DNSServiceRegister(
		&ref,
		0, // flags: 0 lets the daemon auto-rename on a name conflict
		0, // interfaceIndex: 0 = all interfaces
		cName,
		cType,
		nil, // domain: daemon default (local)
		nil, // host: daemon default (this machine)
		C.esi_htons(C.uint16_t(port)),
		C.uint16_t(len(txt)),
		unsafe.Pointer(&txt[0]),
		nil, // no reply callback
		nil, // no context
	)
	if rc != C.kDNSServiceErr_NoError {
		return nil, fmt.Errorf("DNSServiceRegister: Bonjour error %d", int(rc))
	}

	log.Info("mdns advertising (system responder)",
		"service", ServiceType, "instance", instance, "base_url", baseURL)

	var once sync.Once
	return func() {
		once.Do(func() { C.DNSServiceRefDeallocate(ref) })
	}, nil
}

// packTXT encodes DNS-SD TXT entries in wire format: each "key=value" entry is
// prefixed with a single length byte.
func packTXT(entries []string) []byte {
	var buf []byte
	for _, e := range entries {
		if len(e) > 255 {
			e = e[:255]
		}
		buf = append(buf, byte(len(e)))
		buf = append(buf, e...)
	}
	return buf
}
