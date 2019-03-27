// Package tcpinfo contains tools to convert netlink messages to golang structs.
// It contains structs for raw linux route attribute messages related to tcp-info.
package tcpinfo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"syscall"
	"unsafe"

	// Hack to force loading library, which is currently used only in nested test.

	_ "github.com/vishvananda/netlink/nl"
)

var (
	ErrWrongSize = errors.New("Struct size is smaller")
)

// InetDiagSockID is the binary linux representation of a socket, as in linux/inet_diag.h
// Linux code comments indicate this struct uses the network byte order!!!
type InetDiagSockID struct {
	IDiagSPort [2]byte
	IDiagDPort [2]byte
	IDiagSrc   [16]byte
	IDiagDst   [16]byte
	IDiagIf    [4]byte
	// TODO - change this to [2]uint32 ?
	IDiagCookie [8]byte
}

// TODO should use more net.IP code instead of custom code.
func ip(bytes [16]byte) net.IP {
	if isIpv6(bytes) {
		return ipv6(bytes)
	}
	return ipv4(bytes)
}

func isIpv6(original [16]byte) bool {
	for i := 4; i < 16; i++ {
		if original[i] != 0 {
			return true
		}
	}
	return false
}

func ipv4(original [16]byte) net.IP {
	return net.IPv4(original[0], original[1], original[2], original[3]).To4()
}

func ipv6(original [16]byte) net.IP {
	return original[:]
}

func (id *InetDiagSockID) String() string {
	return fmt.Sprintf("%s:%d -> %s:%d", id.SrcIP().String(), id.SPort(), id.DstIP().String(), id.DPort())
}

// Interface returns the interface number.
func (id *InetDiagSockID) Interface() uint32 {
	return binary.BigEndian.Uint32(id.IDiagIf[:])
}

// SrcIP returns a golang net encoding of source address.
func (id *InetDiagSockID) SrcIP() net.IP {
	return ip(id.IDiagSrc)
}

// DstIP returns a golang net encoding of destination address.
func (id *InetDiagSockID) DstIP() net.IP {
	return ip(id.IDiagDst)
}

// SPort returns the host byte ordered port.
// In general, Netlink is supposed to use host byte order, but this seems to be an exception.
// Perhaps Netlink is reading a tcp stack structure that holds the port in network byte order.
func (id *InetDiagSockID) SPort() uint16 {
	return binary.BigEndian.Uint16(id.IDiagSPort[:])
}

// DPort returns the host byte ordered port.
// In general, Netlink is supposed to use host byte order, but this seems to be an exception.
// Perhaps Netlink is reading a tcp stack structure that holds the port in network byte order.
func (id *InetDiagSockID) DPort() uint16 {
	return binary.BigEndian.Uint16(id.IDiagDPort[:])
}

// Cookie returns the SockID's 64 bit unsigned cookie.
func (id *InetDiagSockID) Cookie() uint64 {
	// This is a socket UUID generated within the kernel, and is therefore in host byte order.
	return binary.LittleEndian.Uint64(id.IDiagCookie[:])
}

// InetDiagMsg is the linux binary representation of a InetDiag message header, as in linux/inet_diag.h
// Note that netlink messages use host byte ordering, unless NLA_F_NET_BYTEORDER flag is present.
type InetDiagMsg struct {
	IDiagFamily  uint8
	IDiagState   uint8
	IDiagTimer   uint8
	IDiagRetrans uint8
	ID           InetDiagSockID
	IDiagExpires uint32
	IDiagRqueue  uint32
	IDiagWqueue  uint32
	IDiagUID     uint32
	IDiagInode   uint32
}

// RouteAttrValue is the type of RouteAttr.Value
type RouteAttrValue []byte

type Protocol int32

const (
	Protocol_IPPROTO_UNUSED Protocol = 0
	Protocol_IPPROTO_TCP    Protocol = 6
	Protocol_IPPROTO_UDP    Protocol = 17
	Protocol_IPPROTO_DCCP   Protocol = 33
)

var Protocol_name = map[int32]string{
	0:  "IPPROTO_UNUSED",
	6:  "IPPROTO_TCP",
	17: "IPPROTO_UDP",
	33: "IPPROTO_DCCP",
}

var Protocol_value = map[string]int32{
	"IPPROTO_UNUSED": 0,
	"IPPROTO_TCP":    6,
	"IPPROTO_UDP":    17,
	"IPPROTO_DCCP":   33,
}

// ParseCong returns the congestion algorithm string
func (raw *RouteAttrValue) Cong(rta *syscall.NetlinkRouteAttr) string {
	return string(rta.Value[:len(rta.Value)-1])
}

/*
// AttrToField fills the appropriate proto subfield from a route attribute.
func AttrToStruct(attrType int, attr RouteAttrValue) {
	switch attrType {
	case inetdiag.INET_DIAG_INFO:
		ldiwr := ParseLinuxTCPInfo(attr)
	case inetdiag.INET_DIAG_CONG:
		all.CongestionAlgorithm = ParseCong(rta)
	case inetdiag.INET_DIAG_SHUTDOWN:
		all.Shutdown = &TCPDiagnosticsProto_ShutdownMask{ShutdownMask: uint32(rta.Value[0])}
	case inetdiag.INET_DIAG_MEMINFO:
		memInfo := ParseMemInfo(rta)
		if memInfo != nil {
			all.MemInfo = &MemInfoProto{}
			*all.MemInfo = *memInfo // Copy, to avoid references the attribute
		}
	case inetdiag.INET_DIAG_SKMEMINFO:
		memInfo := ParseSockMemInfo(rta)
		if memInfo != nil {
			all.SocketMem = &SocketMemInfoProto{}
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
*/

// LinuxTCPInfo is the linux defined structure returned in RouteAttr DIAG_INFO messages.
// It corresponds to the struct tcp_info in include/uapi/linux/tcp.h
type LinuxTCPInfo struct {
	State       uint8
	CAState     uint8
	Retransmits uint8
	Probes      uint8
	Backoff     uint8
	Options     uint8
	WScale      uint8 //snd_wscale : 4, tcpi_rcv_wscale : 4;
	AppLimited  uint8 //delivery_rate_app_limited:1;

	RTO    uint32 // offset 8
	ATO    uint32
	SndMSS uint32
	RcvMSS uint32

	Unacked uint32 // offset 24
	Sacked  uint32
	Lost    uint32
	Retrans uint32
	Fackets uint32

	/* Times. */
	// These seem to be elapsed time, so they increase on almost every sample.
	// We can probably use them to get more info about intervals between samples.
	LastDataSent uint32 // offset 44
	LastAckSent  uint32 /* Not remembered, sorry. */ // offset 48
	LastDataRecv uint32 // offset 52
	LastAckRecv  uint32 // offset 56

	/* Metrics. */
	PMTU        uint32
	RcvSsThresh uint32
	RTT         uint32
	RTTVar      uint32
	SndSsThresh uint32
	SndCwnd     uint32
	AdvMSS      uint32
	Reordering  uint32

	RcvRTT   uint32
	RcvSpace uint32

	TotalRetrans uint32

	PacingRate    int64  // This is often -1, so better for it to be signed
	MaxPacingRate int64  // This is often -1, so better to be signed.
	BytesAcked    uint64 /* RFC4898 tcpEStatsAppHCThruOctetsAcked */
	BytesReceived uint64 /* RFC4898 tcpEStatsAppHCThruOctetsReceived */
	SegsOut       uint32 /* RFC4898 tcpEStatsPerfSegsOut */
	SegsIn        uint32 /* RFC4898 tcpEStatsPerfSegsIn */

	NotsentBytes uint32
	MinRTT       uint32
	DataSegsIn   uint32 /* RFC4898 tcpEStatsDataSegsIn */
	DataSegsOut  uint32 /* RFC4898 tcpEStatsDataSegsOut */

	DeliveryRate uint64

	BusyTime      int64 /* Time (usec) busy sending data */
	RWndLimited   int64 /* Time (usec) limited by receive window */
	SndBufLimited int64 /* Time (usec) limited by send buffer */

	Delivered   uint32
	DeliveredCE uint32

	BytesSent    uint64 /* RFC4898 tcpEStatsPerfHCDataOctetsOut */
	BytesRetrans uint64 /* RFC4898 tcpEStatsPerfOctetsRetrans */
	DSackDups    uint32 /* RFC4898 tcpEStatsStackDSACKDups */
	ReordSeen    uint32 /* reordering events seen */
}

// Useful offsets
const (
	LastDataSentOffset = unsafe.Offsetof(LinuxTCPInfo{}.LastDataSent)
	PmtuOffset         = unsafe.Offsetof(LinuxTCPInfo{}.PMTU)
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
	// TODO Check for larger than expected, and increment a metric with appropriate label.
	return unsafe.Pointer(&src[0])
}

// ToLinuxTCPInfo maps the raw RouteAttrValue into a LinuxTCPInfo struct.
// For older data, it may have to copy the bytes.
func (raw RouteAttrValue) ToLinuxTCPInfo() *LinuxTCPInfo {
	structSize := (int)(unsafe.Sizeof(LinuxTCPInfo{}))
	return (*LinuxTCPInfo)(MaybeCopy(raw, structSize))
}

// Haven't found a corresponding linux struct, but the message is described
// in https://manpages.debian.org/stretch/manpages/sock_diag.7.en.html
type SocketMemInfo struct {
	RmemAlloc  uint32
	Rcvbuf     uint32
	WmemAlloc  uint32
	Sndbuf     uint32
	FwdAlloc   uint32
	WmemQueued uint32
	Optmem     uint32
	Backlog    uint32
	Drops      uint32
}

// ToSockMemInfo maps the raw RouteAttrValue onto a SockMemInfo.
// For older data, it may have to copy the bytes.
func (raw RouteAttrValue) ToSockMemInfo() *SocketMemInfo {
	structSize := (int)(unsafe.Sizeof(SocketMemInfo{}))
	return (*SocketMemInfo)(MaybeCopy(raw, structSize))
}

// MemInfo corresponds to the linux struct inet_diag_meminfo.
type MemInfo struct {
	Rmem uint32
	Wmem uint32
	Fmem uint32
	Tmem uint32
}

// ToMemInfo maps the raw RouteAttrValue onto a MemInfo.
func (raw RouteAttrValue) ToMemInfo() *MemInfo {
	structSize := (int)(unsafe.Sizeof(MemInfo{}))
	return (*MemInfo)(MaybeCopy(raw, structSize))
}

// More from include/uapi/linux/inet_diag.h

170 /* INET_DIAG_VEGASINFO */
171
type VegasInfo struct {
	Enabled uint32
	RTTCount uint32
	RTT uint32
	MinRTT uint32
}
type DCTCPInfo struct {
	Enabled uint16
	CEState uint16
	Alpha uint32
	ABEcn uint32
	ABTot uint32
}

// BBRInfo corresponds to linux struct tcp_bbr_info.
type BBRInfo struct {
	Bw         int64  // Max-filtered BW (app throughput) estimate in bytes/second
	MinRtt     uint32  // Min-filtered RTT in uSec
	PacingGain uint32  // Pacing gain shifted left 8 bits
	CwndGain   uint32 // Cwnd gain shifted left 8 bits
}

// ToBBRInfo maps the raw RouteAttrValue onto a BBRInfo.
// For older data, it may have to copy the bytes.
func (raw RouteAttrValue) ToBBRInfo() *BBRInfo {
	structSize := (int)(unsafe.Sizeof(MemInfo{}))
	return (*BBRInfo)(MaybeCopy(raw, structSize))
}

// Parent containing all info gathered through netlink library.
type Wrapper struct {
	// Info from struct inet_diag_msg, including socket_id;
	InetDiagMsg *InetDiagMsg
	// From INET_DIAG_PROTOCOL message.
	DiagProtocol Protocol
	// From INET_DIAG_CONG message.
	CongestionAlgorithm string

	// The following three are mutually exclusive, as they provide
	// data from different congestion control strategies.
	//Vegas *VegasInfo
	BBR *BBRInfo
	//DCTCP *DCTCPInfo

	// Data obtained from INET_DIAG_SKMEMINFO.
	SocketMem *SocketMemInfo

	// Data obtained from INET_DIAG_MEMINFO.
	MemInfo *MemInfo

	// Data obtained from struct tcp_info.
	TcpInfo *LinuxTCPInfo

	// TODO
	// If there is shutdown info, this is the mask value.
	// Check has_shutdown_mask to determine whether present.
	//
	// Types that are valid to be assigned to Shutdown:
	//	*TCPDiagnosticsProto_ShutdownMask
	// Shutdown isTCPDiagnosticsProto_Shutdown

	// Timestamp of batch of messages containing this message.
	Timestamp int64
}
