//go:build windows

package rdpax

import (
	"sync"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// DIID of IMsTscAxEvents, the control's outgoing event interface.
var iidMsTscAxEvents = ole.NewGUID("{336D5562-EFA8-482E-8CB3-C5C0FC7A7DB6}")

// Dispatch IDs of IMsTscAxEvents, read from the mstscax.dll type library.
const (
	dispOnConnecting              = 1
	dispOnConnected               = 2
	dispOnLoginComplete           = 3
	dispOnDisconnected            = 4
	dispOnFatalError              = 10
	dispOnWarning                 = 11
	dispOnRemoteDesktopSizeChange = 12
	dispOnLogonError              = 22
	dispOnAutoReconnected         = 33
	dispOnAutoReconnecting2       = 34
)

const (
	sOK          = 0
	eNotImpl     = 0x80004001
	eNoInterface = 0x80004002
	ePointer     = 0x80004003
)

type dispParams struct {
	rgvarg            *ole.VARIANT
	rgdispidNamedArgs *int32
	cArgs             uint32
	cNamedArgs        uint32
}

type sinkVtbl struct {
	queryInterface   uintptr
	addRef           uintptr
	release          uintptr
	getTypeInfoCount uintptr
	getTypeInfo      uintptr
	getIDsOfNames    uintptr
	invoke           uintptr
}

// eventSink is a minimal IDispatch implementation that the control calls
// back into. The vtbl pointer must be the first field so that a pointer to
// the struct is a valid COM interface pointer.
type eventSink struct {
	vtbl *sinkVtbl
	ref  int32
	fn   func(dispid int32, args []ole.VARIANT)
}

var (
	sinkVtblOnce sync.Once
	sinkVt       sinkVtbl

	// sinks keeps every live sink reachable for the GC and lets the
	// callbacks map a COM this-pointer back to its Go object. Only touched
	// from the STA thread that owns the control.
	sinks = map[uintptr]*eventSink{}
)

func newEventSink(fn func(dispid int32, args []ole.VARIANT)) *eventSink {
	sinkVtblOnce.Do(func() {
		sinkVt = sinkVtbl{
			queryInterface:   syscall.NewCallback(sinkQueryInterface),
			addRef:           syscall.NewCallback(sinkAddRef),
			release:          syscall.NewCallback(sinkRelease),
			getTypeInfoCount: syscall.NewCallback(sinkGetTypeInfoCount),
			getTypeInfo:      syscall.NewCallback(sinkGetTypeInfo),
			getIDsOfNames:    syscall.NewCallback(sinkGetIDsOfNames),
			invoke:           syscall.NewCallback(sinkInvoke),
		}
	})
	s := &eventSink{vtbl: &sinkVt, ref: 1, fn: fn}
	sinks[uintptr(unsafe.Pointer(s))] = s
	return s
}

func (s *eventSink) unknown() *ole.IUnknown {
	return (*ole.IUnknown)(unsafe.Pointer(s))
}

func (s *eventSink) free() {
	delete(sinks, uintptr(unsafe.Pointer(s)))
}

func sinkQueryInterface(this uintptr, riid *ole.GUID, ppv *uintptr) uintptr {
	if ppv == nil {
		return ePointer
	}
	if ole.IsEqualGUID(riid, ole.IID_IUnknown) || ole.IsEqualGUID(riid, ole.IID_IDispatch) || ole.IsEqualGUID(riid, iidMsTscAxEvents) {
		*ppv = this
		sinkAddRef(this)
		return sOK
	}
	*ppv = 0
	return eNoInterface
}

func sinkAddRef(this uintptr) uintptr {
	s := sinks[this]
	if s == nil {
		return 1
	}
	s.ref++
	return uintptr(s.ref)
}

func sinkRelease(this uintptr) uintptr {
	s := sinks[this]
	if s == nil {
		return 0
	}
	s.ref--
	return uintptr(s.ref)
}

func sinkGetTypeInfoCount(this uintptr, count *uint32) uintptr {
	if count != nil {
		*count = 0
	}
	return sOK
}

func sinkGetTypeInfo(this uintptr, index uint32, lcid uint32, typeInfo *uintptr) uintptr {
	return eNotImpl
}

func sinkGetIDsOfNames(this uintptr, riid *ole.GUID, names **uint16, count uint32, lcid uint32, dispids *int32) uintptr {
	return eNotImpl
}

func sinkInvoke(this uintptr, dispid int32, riid *ole.GUID, lcid uint32, flags uint16, params *dispParams, result *ole.VARIANT, excepInfo uintptr, argErr *uint32) uintptr {
	s := sinks[this]
	if s == nil || s.fn == nil {
		return sOK
	}
	var args []ole.VARIANT
	if params != nil && params.cArgs > 0 && params.rgvarg != nil {
		// COM passes arguments in reverse order.
		raw := unsafe.Slice(params.rgvarg, params.cArgs)
		args = make([]ole.VARIANT, len(raw))
		for i := range raw {
			args[len(raw)-1-i] = raw[i]
		}
	}
	s.fn(dispid, args)
	return sOK
}
