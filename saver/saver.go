// Package saver contains all logic for writing records to files.
//  1. Sets up a channel that accepts slices of *netlink.ArchivalRecord
//  2. Maintains a map of Connections, one for each connection.
//  3. Uses several marshallers goroutines to serialize data and and write to
//     zstd files.
//  4. Rotates Connection output files every 10 minutes for long lasting connections.
//  5. uses a cache to detect meaningful state changes, and avoid excessive
//     writes.
package saver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/m-lab/tcp-info/cache"
	"github.com/m-lab/tcp-info/inetdiag"
	"github.com/m-lab/tcp-info/metrics"
	"github.com/m-lab/tcp-info/netlink"
	"github.com/m-lab/tcp-info/tcp"
	"github.com/m-lab/tcp-info/zstd"
	"github.com/m-lab/uuid"
)

// This is the maximum switch/network if speed in bits/sec.  It is used to check for illogical bit rate observations.
const maxSwitchSpeed = 1e10

// We will send an entire batch of prefiltered ArchivalRecords through a channel from
// the collection loop to the top level saver.  The saver will detect new connections
// and significant diffs, maintain the connection cache, determine
// how frequently to save deltas for each connection.
//
// The saver will use a small set of Marshallers to convert to protos,
// marshal the protos, and write them to files.

// Errors generated by saver functions.
var (
	ErrNoMarshallers = errors.New("Saver has zero Marshallers")
)

// Task represents a single marshalling task, specifying the message and the writer.
type Task struct {
	// nil message means close the writer.
	Message *netlink.ArchivalRecord
	Writer  io.WriteCloser
}

// CacheLogger is any object with a LogCacheStats method.
type CacheLogger interface {
	LogCacheStats(localCount, errCount int)
}

// MarshalChan is a channel of marshalling tasks.
type MarshalChan chan<- Task

func runMarshaller(taskChan <-chan Task, wg *sync.WaitGroup) {
	for {
		task, ok := <-taskChan
		if !ok {
			break
		}
		if task.Message == nil {
			task.Writer.Close()
			continue
		}
		if task.Writer == nil {
			log.Fatal("Nil writer")
		}
		b, _ := json.Marshal(task.Message) // FIXME: don't ignore error
		task.Writer.Write(b)
		task.Writer.Write([]byte("\n"))
	}
	log.Println("Marshaller Done")
	wg.Done()
}

func newMarshaller(wg *sync.WaitGroup) MarshalChan {
	marshChan := make(chan Task, 100)
	wg.Add(1)
	go runMarshaller(marshChan, wg)
	return marshChan
}

// Connection objects handle all output associated with a single connection.
type Connection struct {
	Inode      uint32 // TODO - also use the UID???
	ID         inetdiag.SockID
	UID        uint32
	Slice      string    // 4 hex, indicating which machine segment this is on.
	StartTime  time.Time // Time the connection was initiated.
	Sequence   int       // Typically zero, but increments for long running connections.
	Expiration time.Time // Time we will swap files and increment Sequence.
	Writer     io.WriteCloser
}

func newConnection(info *inetdiag.InetDiagMsg, timestamp time.Time) *Connection {
	conn := Connection{Inode: info.IDiagInode, ID: info.ID.GetSockID(), UID: info.IDiagUID, Slice: "", StartTime: timestamp, Sequence: 0,
		Expiration: time.Now()}
	return &conn
}

// Rotate opens the next writer for a connection.
func (conn *Connection) Rotate(Host string, Pod string, FileAgeLimit time.Duration) error {
	datePath := conn.StartTime.Format("2006/01/02")
	err := os.MkdirAll(datePath, 0777)
	if err != nil {
		return err
	}
	id := uuid.FromCookie(conn.ID.CookieUint64())
	conn.Writer, err = zstd.NewWriter(fmt.Sprintf("%s/%s.%05d.jsonl.zst", datePath, id, conn.Sequence))
	if err != nil {
		return err
	}
	conn.writeHeader()
	metrics.NewFileCount.Inc()
	conn.Expiration = conn.Expiration.Add(10 * time.Minute)
	conn.Sequence++
	return nil
}

func (conn *Connection) writeHeader() {
	msg := netlink.ArchivalRecord{
		Metadata: &netlink.Metadata{
			UUID:      uuid.FromCookie(conn.ID.CookieUint64()),
			Sequence:  conn.Sequence,
			StartTime: conn.StartTime,
		},
	}
	// FIXME: Error handling
	bytes, _ := json.Marshal(msg)
	conn.Writer.Write(bytes)
	conn.Writer.Write([]byte("\n"))
}

type stats struct {
	TotalCount   int64
	NewCount     int64
	DiffCount    int64
	ExpiredCount int64
}

func (s *stats) IncTotalCount() {
	atomic.AddInt64(&s.TotalCount, 1)
}

func (s *stats) IncNewCount() {
	atomic.AddInt64(&s.NewCount, 1)
}

func (s *stats) IncDiffCount() {
	atomic.AddInt64(&s.DiffCount, 1)
}

func (s *stats) IncExpiredCount() {
	atomic.AddInt64(&s.ExpiredCount, 1)
}

func (s *stats) Copy() stats {
	result := stats{}
	result.TotalCount = atomic.LoadInt64(&s.TotalCount)
	result.NewCount = atomic.LoadInt64(&s.NewCount)
	result.DiffCount = atomic.LoadInt64(&s.DiffCount)
	result.ExpiredCount = atomic.LoadInt64(&s.ExpiredCount)
	return result
}

// Print prints out some basic stats about saver use.
// TODO - should also export all of these as Prometheus metrics.  (Issue #32)
func (s *stats) Print() {
	log.Printf("Cache info total %d same %d diff %d new %d closed %d\n",
		s.TotalCount, s.TotalCount-(s.NewCount+s.DiffCount),
		s.DiffCount, s.NewCount, s.ExpiredCount)
}

// Saver provides functionality for saving tcpinfo diffs to connection files.
// It handles arbitrary connections, and only writes to file when the significant fields
// change.  (TODO - what does "significant fields" mean).
// TODO - just export an interface, instead of the implementation.
type Saver struct {
	Host         string // mlabN
	Pod          string // 3 alpha + 2 decimal
	FileAgeLimit time.Duration
	MarshalChans []MarshalChan
	Done         *sync.WaitGroup // All marshallers will call Done on this.
	Connections  map[uint64]*Connection

	cache *cache.Cache
	stats stats
}

// NewSaver creates a new Saver for the given host and pod.  numMarshaller controls
// how many marshalling goroutines are used to distribute the marshalling workload.
func NewSaver(host string, pod string, numMarshaller int) *Saver {
	m := make([]MarshalChan, 0, numMarshaller)
	c := cache.NewCache()
	// We start with capacity of 500.  This will be reallocated as needed, but this
	// is not a performance concern.
	conn := make(map[uint64]*Connection, 500)
	wg := &sync.WaitGroup{}
	ageLim := 10 * time.Minute

	for i := 0; i < numMarshaller; i++ {
		m = append(m, newMarshaller(wg))
	}

	return &Saver{
		Host:         host,
		Pod:          pod,
		FileAgeLimit: ageLim,
		MarshalChans: m,
		Done:         wg,
		Connections:  conn,
		cache:        c,
	}
}

// queue queues a single ArchivalRecord to the appropriate marshalling queue, based on the
// connection Cookie.
func (svr *Saver) queue(msg *netlink.ArchivalRecord) error {
	idm, err := msg.RawIDM.Parse()
	if err != nil {
		log.Println(err)
		// TODO error metric
	}
	cookie := idm.ID.Cookie()
	if cookie == 0 {
		return errors.New("Cookie = 0")
	}
	if len(svr.MarshalChans) < 1 {
		return ErrNoMarshallers
	}
	q := svr.MarshalChans[int(cookie%uint64(len(svr.MarshalChans)))]
	conn, ok := svr.Connections[cookie]
	if !ok {
		// Likely first time we have seen this connection.  Create a new Connection, unless
		// the connection is already closing.
		if idm.IDiagState >= uint8(tcp.FIN_WAIT1) {
			log.Println("Skipping", tcp.State(idm.IDiagState).String(), idm, msg.Timestamp)
			return nil
		}
		if svr.cache.CycleCount() > 0 || idm.IDiagState != uint8(tcp.ESTABLISHED) {
			log.Println("New conn:", cookie, idm.ID.GetSockID(), msg.Timestamp)
		}
		conn = newConnection(idm, msg.Timestamp)
		svr.Connections[cookie] = conn
	} else {
		//log.Println("Diff inode:", inode)
	}
	if time.Now().After(conn.Expiration) && conn.Writer != nil {
		q <- Task{nil, conn.Writer} // Close the previous file.
		conn.Writer = nil
	}
	if conn.Writer == nil {
		err := conn.Rotate(svr.Host, svr.Pod, svr.FileAgeLimit)
		if err != nil {
			return err
		}
	}
	q <- Task{msg, conn.Writer}
	return nil
}

func (svr *Saver) endConn(cookie uint64) {
	log.Println("Closing:", cookie)
	q := svr.MarshalChans[cookie%uint64(len(svr.MarshalChans))]
	conn, ok := svr.Connections[cookie]
	if ok && conn.Writer != nil {
		q <- Task{nil, conn.Writer}
		delete(svr.Connections, cookie)
	}
}

// Handle a bundle of messages.
// Returns the bytes sent and received on all non-local connections.
func (svr *Saver) handleType(t time.Time, msgs []*netlink.NetlinkMessage) (uint64, uint64) {
	var liveSent, liveReceived uint64
	for _, msg := range msgs {
		// In swap and queue, we want to track the total speed of all connections
		// every second.
		if msg == nil {
			log.Println("Nil message")
			continue
		}
		ar, err := netlink.MakeArchivalRecord(msg, true)
		if ar == nil {
			if err != nil {
				log.Println(err)
			}
			continue
		}
		ar.Timestamp = t

		// Note: If GetStats shows up in profiling, might want to move to once/second code.
		s, r := ar.GetStats()
		liveSent += s
		liveReceived += r
		svr.swapAndQueue(ar)
	}

	return liveSent, liveReceived
}

type cycleStats struct {
	live4, live6 uint64
	closed       uint64
}

func (cs cycleStats) total() uint64 {
	return cs.live4 + cs.live6 + cs.closed
}

// returns true if ok, false if problem.
func (cs cycleStats) compare(msg string, prev cycleStats) bool {
	if cs.closed < prev.closed {
		log.Printf("%s: %+v -> %+v\n", msg, prev, cs)
		return false
	}
	if cs.total() < prev.total() {
		log.Printf("%s: %+v -> %+v (%d > %d)\n", msg, prev, cs, prev.total(), cs.total())
		return false
	}
	return true
}

// MessageSaverLoop runs a loop to receive batches of ArchivalRecords.  Local connections
func (svr *Saver) MessageSaverLoop(readerChannel <-chan netlink.MessageBlock) {
	log.Println("Starting Saver")

	var lastSent, lastReceived cycleStats
	var reportedSent, reportedReceived uint64
	var sentClosed, receivedClosed uint64 // Totals of ALL closed connections.
	lastReportTime := time.Time{}.Unix()

	for {
		msgs, ok := <-readerChannel
		if !ok {
			break
		}

		var sent, received cycleStats

		// Handle v4 and v6 messages, and return the total bytes sent and received.
		// TODO - we only need to collect these stats if this is a reporting cycle.
		sent.live4, received.live4 = svr.handleType(msgs.V4Time, msgs.V4Messages)
		sent.live6, received.live6 = svr.handleType(msgs.V6Time, msgs.V6Messages)

		// Note that the connections that have closed may have had traffic that
		// we never see, and therefore can't account for in metrics.
		residual := svr.cache.EndCycle()

		// Accumulator for byte counts from closed connections.
		var rs uint64
		var rr uint64

		// Remove all missing connections from the cache.
		// Also keep a metric of the total cumulative send and receive bytes.
		for cookie := range residual {
			// residual is the list of all keys that were not updated.
			s, r := residual[cookie].GetStats()
			rs += s
			rr += r
			svr.endConn(cookie)
			svr.stats.IncExpiredCount()
		}

		sentClosed += rs
		receivedClosed += rr

		sent.closed = sentClosed
		received.closed = receivedClosed

		sent.compare("sent", lastSent)
		received.compare("received", lastReceived)

		// Every second, update the total throughput for the past second.
		if msgs.V4Time.Unix() > lastReportTime {

			// NOTE: We are seeing occasions when total < reported.  This messes up prometheus, so
			// we detect that and skip reporting.
			// This seems to be persistent, not just a momentary glitch.  The total may drop by 500KB,
			// and only recover after many seconds of gradual increases (on idle workstation).
			// This workaround seems to also cure the 2<<67 reports.
			// We also check for increments larger than 10x the maxSwitchSpeed.
			totalSent := sent.total()
			if totalSent > 10*maxSwitchSpeed/8+reportedSent || totalSent < reportedSent {
				// Some bug in the accounting!!
				log.Println("Skipping BytesSent report due to bad accounting")
				if totalSent < reportedSent {
					metrics.ErrorCount.WithLabelValues("totalSent < reportedSent").Inc()
				} else {
					metrics.ErrorCount.WithLabelValues("totalSent-reportedSent exceeds network capacity").Inc()
				}
			} else {
				metrics.SendRateHistogram.Observe(8 * float64(totalSent-reportedSent))
				reportedSent = totalSent // the total bytes reported to prometheus.
			}

			totalReceived := received.total()
			if totalReceived > 10*maxSwitchSpeed/8+reportedReceived || totalReceived < reportedReceived {
				// Some bug in the accounting!!
				log.Println("Skipping BytesReceived report due to bad accounting")
				if totalReceived < reportedReceived {
					metrics.ErrorCount.WithLabelValues("totalReceived < reportedReceived").Inc()
				} else {
					metrics.ErrorCount.WithLabelValues("totalReceived-reportedReceived exceeds network capacity").Inc()
				}
			} else {
				metrics.ReceiveRateHistogram.Observe(8 * float64(totalReceived-reportedReceived))
				reportedReceived = totalReceived // the total bytes reported to prometheus.
			}

			lastReportTime = msgs.V4Time.Unix()
		}

		lastSent = sent
		lastReceived = received
	}

	svr.Close()
}

func (svr *Saver) swapAndQueue(pm *netlink.ArchivalRecord) {
	svr.stats.IncTotalCount() // TODO fix race
	old, err := svr.cache.Update(pm)
	if err != nil {
		// TODO metric
		log.Println(err)
		return
	}
	if old == nil {
		svr.stats.IncNewCount()
		metrics.SnapshotCount.Inc()
		err := svr.queue(pm)
		if err != nil {
			log.Println(err)
			log.Println("Connections", len(svr.Connections))
		}
	} else {
		oldIDM, err := old.RawIDM.Parse()
		if err != nil {
			// TODO metric
			log.Println(err)
			return
		}
		pmIDM, err := pm.RawIDM.Parse()
		if err != nil {
			// TODO metric
			log.Println(err)
			return
		}
		if oldIDM.ID != pmIDM.ID {
			log.Println("Mismatched SockIDs", oldIDM.ID, pmIDM.ID)
		}
		change, err := pm.Compare(old)
		if err != nil {
			// TODO metric
			log.Println(err)
			return
		}
		if change > netlink.NoMajorChange {
			svr.stats.IncDiffCount()
			metrics.SnapshotCount.Inc()
			err := svr.queue(pm)
			if err != nil {
				// TODO metric
				log.Println(err)
			}
		}
	}
}

// Close shuts down all the marshallers, and waits for all files to be closed.
func (svr *Saver) Close() {
	log.Println("Terminating Saver")
	log.Println("Total of", len(svr.Connections), "connections active.")
	for i := range svr.Connections {
		svr.endConn(i)
	}
	log.Println("Closing Marshallers")
	for i := range svr.MarshalChans {
		close(svr.MarshalChans[i])
	}
	svr.Done.Wait()
}

// LogCacheStats prints out some basic cache stats.
// TODO - should also export all of these as Prometheus metrics.  (Issue #32)
func (svr *Saver) LogCacheStats(localCount, errCount int) {
	stats := svr.stats.Copy() // Get a copy
	log.Printf("Cache info total %d  local %d same %d diff %d new %d err %d\n",
		stats.TotalCount+(int64)(localCount), localCount,
		stats.TotalCount-((int64)(errCount)+stats.NewCount+stats.DiffCount+(int64)(localCount)),
		stats.DiffCount, stats.NewCount, errCount)
}
