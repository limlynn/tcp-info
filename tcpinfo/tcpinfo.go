// Package tcpinfo contains tools to convert netlink messages to golang structs.
// It contains structs for raw linux route attribute messages related to tcp-info.
package tcpinfo

import (
	"bytes"
	"errors"
	"log"
	"syscall"
	"unsafe"

	"github.com/m-lab/tcp-info/inetdiag"

	// Hack to force loading library, which is currently used only in nested test.
	_ "github.com/vishvananda/netlink/nl"
)

var (
	ErrWrongSize = errors.New("Struct size is smaller")
)

// RouteAttrValue is the type of RouteAttr.Value
type RouteAttrValue []byte

func (raw RouteAttrValue) ToTCPInfo() (*LinuxTCPInfo, error) {
	size := int(unsafe.Sizeof(LinuxTCPInfo{}))
	if len(raw) != size {
		return nil, ErrWrongSize
	}
	return (*syscall.NlMsghdr)(unsafe.Pointer(&raw[0])), nil

}

// ParseCong returns the congestion algorithm string
func ParseCong(rta *syscall.NetlinkRouteAttr) string {
	return string(rta.Value[:len(rta.Value)-1])
}

// AttrToField fills the appropriate proto subfield from a route attribute.
func AttrToStruct(attrType int, attr inetdiag.RouteAttrValue) {
	switch attrType {
	case inetdiag.INET_DIAG_INFO:
		ldiwr := ParseLinuxTCPInfo(attr)
	case inetdiag.INET_DIAG_CONG:
		all.CongestionAlgorithm = ParseCong(rta)
	case inetdiag.INET_DIAG_SHUTDOWN:
		all.Shutdown = &tcpinfo.TCPDiagnosticsProto_ShutdownMask{ShutdownMask: uint32(rta.Value[0])}
	case inetdiag.INET_DIAG_MEMINFO:
		memInfo := ParseMemInfo(rta)
		if memInfo != nil {
			all.MemInfo = &tcpinfo.MemInfoProto{}
			*all.MemInfo = *memInfo // Copy, to avoid references the attribute
		}
	case inetdiag.INET_DIAG_SKMEMINFO:
		memInfo := ParseSockMemInfo(rta)
		if memInfo != nil {
			all.SocketMem = &tcpinfo.SocketMemInfoProto{}
			*all.SocketMem = *memInfo // Copy, to avoid references the attribute
		}
	case inetdiag.INET_DIAG_TOS:
		// TODO - already seeing these.  Issue #10
	case inetdiag.INET_DIAG_TCLASS:
		// TODO - already seeing these.  Issue #10

	// We are not seeing these so far.  Should implement BBRINFO soon though.
	case inetdiag.INET_DIAG_BBRINFO:
		fallthrough
	case inetdiag.INET_DIAG_VEGASINFO:
		fallthrough
	case inetdiag.INET_DIAG_SKV6ONLY:
		log.Printf("WARNING: Not processing %+v\n", rta)

	case inetdiag.INET_DIAG_MARK:
		// TODO Already seeing this when run as root, so we should process it.
	// TODO case inetdiag.INET_DIAG_PROTOCOL:
	//   Used only for multicast messages. Not expected for our use cases.
	default:
		log.Printf("WARNING: Not processing %+v\n", rta)
		// TODO(gfr) - should LOG(WARNING) on missing cases.
	}
}

// LinuxTCPInfo is the linux defined structure returned in RouteAttr DIAG_INFO messages.
// It corresponds to the struct tcp_info in include/uapi/linux/tcp.h
// TODO should these all be unexported?
// TODO Alternatively, should they be in their own package, with exported fields?
type LinuxTCPInfo struct {
	state       uint8
	caState     uint8
	retransmits uint8
	probes      uint8
	backoff     uint8
	options     uint8
	wscale      uint8 //snd_wscale : 4, tcpi_rcv_wscale : 4;
	appLimited  uint8 //delivery_rate_app_limited:1;

	rto    uint32 // offset 8
	ato    uint32
	sndMss uint32
	rcvMss uint32

	unacked uint32 // offset 24
	sacked  uint32
	lost    uint32
	retrans uint32
	fackets uint32

	/* Times. */
	// These seem to be elapsed time, so they increase on almost every sample.
	// We can probably use them to get more info about intervals between samples.
	lastDataSent uint32 // offset 44
	lastAckSent  uint32 /* Not remembered, sorry. */ // offset 48
	lastDataRecv uint32 // offset 52
	lastAckRecv  uint32 // offset 56

	/* Metrics. */
	pmtu        uint32
	rcvSsThresh uint32
	rtt         uint32
	rttvar      uint32
	sndSsThresh uint32
	sndCwnd     uint32
	advmss      uint32
	reordering  uint32

	rcvRtt   uint32
	rcvSpace uint32

	totalRetrans uint32

	pacingRate    int64  // This is often -1, so better for it to be signed
	maxPacingRate int64  // This is often -1, so better to be signed.
	bytesAcked    uint64 /* RFC4898 tcpEStatsAppHCThruOctetsAcked */
	bytesReceived uint64 /* RFC4898 tcpEStatsAppHCThruOctetsReceived */
	segsOut       uint32 /* RFC4898 tcpEStatsPerfSegsOut */
	segsIn        uint32 /* RFC4898 tcpEStatsPerfSegsIn */

	notsentBytes uint32
	minRtt       uint32
	dataSegsIn   uint32 /* RFC4898 tcpEStatsDataSegsIn */
	dataSegsOut  uint32 /* RFC4898 tcpEStatsDataSegsOut */

	deliveryRate uint64

	busyTime      int64 /* Time (usec) busy sending data */
	rwndLimited   int64 /* Time (usec) limited by receive window */
	sndbufLimited int64 /* Time (usec) limited by send buffer */

	delivered   uint32
	deliveredCe uint32

	bytesSent    uint64 /* RFC4898 tcpEStatsPerfHCDataOctetsOut */
	bytesRetrans uint64 /* RFC4898 tcpEStatsPerfOctetsRetrans */
	dsackDups    uint32 /* RFC4898 tcpEStatsStackDSACKDups */
	reordSeen    uint32 /* reordering events seen */
}

// Useful offsets
const (
	LastDataSentOffset = unsafe.Offsetof(LinuxTCPInfo{}.lastDataSent)
	PmtuOffset         = unsafe.Offsetof(LinuxTCPInfo{}.pmtu)
)

// MaybeCopy checks whether the src is the full size of the intended struct size.
// If so, it just returns the pointer, otherwise it copies the content to an
// appropriately sized new byte slice, and returns pointer to that.
func MaybeCopy(src []byte, size int) unsafe.Pointer {
	if len(src) < size {
		data := make([]byte, size)
		copy(data, src)
		return unsafe.Pointer(&data[0])
	}
	return unsafe.Pointer(&src[0])
}

// ParseLinuxTCPInfo maps the rta Value onto a TCPInfo struct.  It may have to copy the
// bytes.
func ParseLinuxTCPInfo(rta *syscall.NetlinkRouteAttr) *LinuxTCPInfo {
	structSize := (int)(unsafe.Sizeof(LinuxTCPInfo{}))
	data := rta.Value
	//log.Println(len(rta.Value), "vs", structSize)
	if len(rta.Value) < structSize {
		// log.Println(len(rta.Value), "vs", structSize)
		data = make([]byte, structSize)
		copy(data, rta.Value)
	}
	return (*LinuxTCPInfo)(unsafe.Pointer(&data[0]))
}

// ParseSockMemInfo maps the rta Value onto a SockMemInfoProto.
// Since this struct is very simple, it can be mapped directly, instead of using an
// intermediate struct.
func ParseSockMemInfo(rta *syscall.NetlinkRouteAttr) *tcpinfo.SocketMemInfoProto {
	structSize := (int)(unsafe.Sizeof(tcpinfo.SocketMemInfoProto{}))
	return (*tcpinfo.SocketMemInfoProto)(MaybeCopy(rta.Value, structSize))
}

// ParseMemInfo maps the rta Value onto a MemInfoProto.
// Since this struct is very simple, it can be mapped directly, instead of using an
// intermediate struct.
func ParseMemInfo(rta *syscall.NetlinkRouteAttr) *tcpinfo.MemInfoProto {
	structSize := (int)(unsafe.Sizeof(tcpinfo.MemInfoProto{}))
	return (*tcpinfo.MemInfoProto)(MaybeCopy(rta.Value, structSize))
}

// ChangeType indicates why a new record is worthwhile saving.
type ChangeType int

const (
	NoMajorChange        ChangeType = iota
	IDiagStateChange                // The IDiagState changed
	NoTCPInfo                       // There is no TCPInfo attribute
	NewAttribute                    // There is a new attribute
	LostAttribute                   // There is a dropped attribute
	AttributeLength                 // The length of an attribute changed
	StateOrCounterChange            // One of the early fields in DIAG_INFO changed.
	PacketCountChange               // One of the packet/byte/segment counts (or other late field) changed
	Other                           // Some other attribute changed
)

// Compare compares important fields to determine whether significant updates have occurred.
// We ignore a bunch of fields:
//  * The TCPInfo fields matching last_* are rapidly changing, but don't have much significance.
//    Are they elapsed time fields?
//  * The InetDiagMsg.Expires is also rapidly changing in many connections, but also seems
//    unimportant.
//
// Significant updates are reflected in the packet, segment and byte count updates, so we
// generally want to record a snapshot when any of those change.  They are in the latter
// part of the linux struct, following the pmtu field.
//
// The simplest test that seems to tell us what we care about is to look at all the fields
// in the TCPInfo struct related to packets, bytes, and segments.  In addition to the TCPState
// and CAState fields, these are probably adequate, but we also check for new or missing attributes
// and any attribute difference outside of the TCPInfo (INET_DIAG_INFO) attribute.
// TODO:
//  Consider moving this function, together with LinuxTCPInfo, into another package depending only on
//  inetdiag. However, that would require exporting all fields of LinuxTCPInfo, which is not
//  necessary if we keep this here.
func Compare(previous *inetdiag.ParsedMessage, current *inetdiag.ParsedMessage) ChangeType {
	// If the TCP state has changed, that is important!
	if previous.InetDiagMsg.IDiagState != current.InetDiagMsg.IDiagState {
		return IDiagStateChange
	}

	// TODO - should we validate that ID matches?  Otherwise, we shouldn't even be comparing the rest.

	a := previous.Attributes[inetdiag.INET_DIAG_INFO]
	b := current.Attributes[inetdiag.INET_DIAG_INFO]
	if a == nil || b == nil {
		return NoTCPInfo
	}

	// If any of the byte/segment/package counters have changed, that is what we are most
	// interested in.
	if 0 != bytes.Compare(a.Value[PmtuOffset:], b.Value[PmtuOffset:]) {
		return StateOrCounterChange
	}

	// Check all the earlier fields, too.  Usually these won't change unless the counters above
	// change, but this way we won't miss something subtle.
	if 0 != bytes.Compare(a.Value[:LastDataSentOffset], b.Value[:LastDataSentOffset]) {
		return StateOrCounterChange
	}

	// If any attributes have been added or removed, that is likely significant.
	for tp := range previous.Attributes {
		switch tp {
		case inetdiag.INET_DIAG_INFO:
			// Handled explicitly above.
		default:
			// Detect any change in anything other than INET_DIAG_INFO
			a := previous.Attributes[tp]
			b := current.Attributes[tp]
			if a == nil && b != nil {
				return NewAttribute
			}
			if a != nil && b == nil {
				return LostAttribute
			}
			if a == nil && b == nil {
				continue
			}
			if len(a.Value) != len(b.Value) {
				return AttributeLength
			}
			// All others we want to be identical
			if 0 != bytes.Compare(a.Value, b.Value) {
				return Other
			}
		}
	}

	return NoMajorChange
}
