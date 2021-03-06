package tracker

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"sync"
	"syscall"
	"testing"

	"github.com/anacrolix/torrent/util"
)

// Ensure net.IPs are stored big-endian, to match the way they're read from
// the wire.
func TestNetIPv4Bytes(t *testing.T) {
	ip := net.IP([]byte{127, 0, 0, 1})
	if ip.String() != "127.0.0.1" {
		t.FailNow()
	}
	if string(ip) != "\x7f\x00\x00\x01" {
		t.Fatal([]byte(ip))
	}
}

func TestMarshalAnnounceResponse(t *testing.T) {
	w := bytes.Buffer{}
	peers := util.CompactPeers{{[4]byte{127, 0, 0, 1}, 2}, {[4]byte{255, 0, 0, 3}, 4}}
	err := peers.WriteBinary(&w)
	if err != nil {
		t.Fatalf("error writing udp announce response addrs: %s", err)
	}
	if w.String() != "\x7f\x00\x00\x01\x00\x02\xff\x00\x00\x03\x00\x04" {
		t.FailNow()
	}
	if binary.Size(AnnounceResponseHeader{}) != 12 {
		t.FailNow()
	}
}

// Failure to write an entire packet to UDP is expected to given an error.
func TestLongWriteUDP(t *testing.T) {
	l, err := net.ListenUDP("udp", nil)
	defer l.Close()
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, l.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for msgLen := 1; ; msgLen *= 2 {
		n, err := c.Write(make([]byte, msgLen))
		if err != nil {
			err := err.(*net.OpError).Err
			if err != syscall.EMSGSIZE {
				t.Fatalf("write error isn't EMSGSIZE: %s", err)
			}
			return
		}
		if n < msgLen {
			t.FailNow()
		}
	}
}

func TestShortBinaryRead(t *testing.T) {
	var data ResponseHeader
	err := binary.Read(bytes.NewBufferString("\x00\x00\x00\x01"), binary.BigEndian, &data)
	if err != io.ErrUnexpectedEOF {
		t.FailNow()
	}
}

func TestConvertInt16ToInt(t *testing.T) {
	i := 50000
	if int(uint16(int16(i))) != 50000 {
		t.FailNow()
	}
}

func TestUDPTracker(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	tr, err := New("udp://tracker.openbittorrent.com:80/announce")
	if err != nil {
		t.Skip(err)
	}
	if err := tr.Connect(); err != nil {
		t.Skip(err)
	}
	req := AnnounceRequest{
		NumWant: -1,
		Event:   Started,
	}
	rand.Read(req.PeerId[:])
	copy(req.InfoHash[:], []uint8{0xa3, 0x56, 0x41, 0x43, 0x74, 0x23, 0xe6, 0x26, 0xd9, 0x38, 0x25, 0x4a, 0x6b, 0x80, 0x49, 0x10, 0xa6, 0x67, 0xa, 0xc1})
	_, err = tr.Announce(&req)
	if err != nil {
		t.Skip(err)
	}
}

// TODO: Create a fake UDP tracker to make these requests to.
func TestAnnounceRandomInfoHash(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	req := AnnounceRequest{
		Event: Stopped,
	}
	rand.Read(req.PeerId[:])
	rand.Read(req.InfoHash[:])
	wg := sync.WaitGroup{}
	for _, url := range []string{
		"udp://tracker.openbittorrent.com:80/announce",
		"udp://tracker.publicbt.com:80",
		"udp://tracker.istole.it:6969",
		"udp://tracker.ccc.de:80",
		"udp://tracker.open.demonii.com:1337",
	} {
		go func(url string) {
			defer wg.Done()
			tr, err := New(url)
			if err != nil {
				t.Fatal(err)
			}
			if err := tr.Connect(); err != nil {
				t.Log(err)
				return
			}
			resp, err := tr.Announce(&req)
			if err != nil {
				t.Logf("error announcing to %s: %s", url, err)
				return
			}
			if resp.Leechers != 0 || resp.Seeders != 0 || len(resp.Peers) != 0 {
				t.Fatal(resp)
			}
		}(url)
		wg.Add(1)
	}
	wg.Wait()
}

// Check that URLPath option is done correctly.
func TestURLPathOption(t *testing.T) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	cl := newClient(&url.URL{
		Host: conn.LocalAddr().String(),
		Path: "/announce",
	})
	go func() {
		err = cl.Connect()
		if err != nil {
			t.Fatal(err)
		}
		log.Print("connected")
		_, err = cl.Announce(&AnnounceRequest{})
		if err != nil {
			t.Fatal(err)
		}
	}()
	var b [512]byte
	_, addr, _ := conn.ReadFrom(b[:])
	r := bytes.NewReader(b[:])
	var h RequestHeader
	read(r, &h)
	w := &bytes.Buffer{}
	write(w, ResponseHeader{
		TransactionId: h.TransactionId,
	})
	write(w, ConnectionResponse{42})
	conn.WriteTo(w.Bytes(), addr)
	n, _, _ := conn.ReadFrom(b[:])
	r = bytes.NewReader(b[:n])
	read(r, &h)
	read(r, &AnnounceRequest{})
	all, _ := ioutil.ReadAll(r)
	if string(all) != "\x02\x09/announce" {
		t.FailNow()
	}
	w = &bytes.Buffer{}
	write(w, ResponseHeader{
		TransactionId: h.TransactionId,
	})
	write(w, AnnounceResponseHeader{})
	conn.WriteTo(w.Bytes(), addr)
}
