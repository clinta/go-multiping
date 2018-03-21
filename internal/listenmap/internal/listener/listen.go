package listener

import (
	"context"
	"errors"
	"net"
	"time"

	"golang.org/x/net/icmp"

	"github.com/TrilliumIT/go-multiping/internal/listenmap/internal/messages"
	"github.com/TrilliumIT/go-multiping/ping"
)

// Listener is a listener for one protocol, ipv4 or ipv6
type Listener struct {
	proto int
	conn  *icmp.PacketConn
	Props *messages.Props
	addL  chan *runParams
	delL  chan struct{}
	// try to lock for sending
	// if returned f is not nil, call f to unlock
	// if f is nil then i is zero and listener is not running
	lockL       chan chan<- func()
	angryPacket []byte
}

// New returns a new listener
func New(p int) *Listener {
	l := &Listener{
		proto: p,
		addL:  make(chan *runParams),
		delL:  make(chan struct{}),
		lockL: make(chan chan<- func()),
	}
	switch p {
	case 4:
		l.Props = messages.V4Props
	case 6:
		l.Props = messages.V6Props
	}

	l.angryPacket, _ = (&icmp.Message{
		Code: 0,
		Type: l.Props.RecvType,
		Body: &icmp.Echo{
			ID:  0,
			Seq: 0,
		},
	}).Marshal(nil)

	go func() {
		var err error
		var i int
		pause := make(chan struct{})
		unpause := func() { pause <- struct{}{} }
		cancelAndWait := func() {}
		for {
			select {
			case r := <-l.lockL:
				if i == 0 {
					r <- nil
					continue
				}
				r <- unpause
				<-pause
			case r := <-l.addL:
				i += 1
				if i == 1 {
					cancelAndWait, err = l.run(r.getCb, r.workers, r.buffer)
				}
				r.err <- err
				err = nil
			case <-l.delL:
				i -= 1
				if i < 0 {
					panic("listener decremented below 0")
				}
				if i == 0 {
					// shut her down
					cancelAndWait()
				}
			}
		}
	}()
	return l
}

// ErrNotRunning is returned if send is requested and listener is not running
var ErrNotRunning = errors.New("listener not running")

// Send sends a packet using this connectiong
func (l *Listener) Send(p *ping.Ping, dst net.Addr) error {
	c := make(chan func())
	l.lockL <- c
	unlock := <-c
	if unlock == nil {
		return ErrNotRunning
	}
	err := l.send(p, dst)
	unlock()
	return err
}

func (l *Listener) send(p *ping.Ping, dst net.Addr) error {
	p.Sent = time.Now()
	b, err := p.ToICMPMsg()
	if err != nil {
		return err
	}
	p.Len, err = l.conn.WriteTo(b, dst)
	return err
}

// Run either starts the listner, or adds another waiter to prevent it from stopping
func (l *Listener) Run(getCb func(net.IP, uint16) func(context.Context, *ping.Ping), workers int, buffer int) (func(), error) {
	done := func() { l.delL <- struct{}{} }
	eCh := make(chan error)
	l.addL <- &runParams{getCb, workers, buffer, eCh}
	return done, <-eCh
}

type runParams struct {
	getCb   func(net.IP, uint16) func(context.Context, *ping.Ping)
	workers int
	buffer  int
	err     chan<- error
}

func (l *Listener) run(getCb func(net.IP, uint16) func(context.Context, *ping.Ping), workers int, buffer int) (cancelAndWait func(), err error) {
	cancelAndWait = func() {}
	l.conn, err = icmp.ListenPacket(l.Props.Network, l.Props.Src)
	if err != nil {
		return cancelAndWait, err
	}
	err = setPacketCon(l.conn)
	if err != nil {
		_ = l.conn.Close()
		return cancelAndWait, err
	}

	// this is not inheriting a context, this thread will exit by cancelandwait
	wCtx, wCancel := context.WithCancel(context.Background())

	// start workers
	wWait := l.startWorkers(wCtx, workers, buffer, getCb)

	cancelAndWait = func() {
		wCancel() // stop workers
		// Despite https://golang.org/pkg/net/#PacketConn claims
		// close does not actually cause reads to be unblocked.
		// This leads to nasty deadlocks.
		// Throw angry packets at the connection until it dies!
		wCh := make(chan struct{})
		go func() { wWait(); close(wCh) }()
	angryPackets:
		for {
			select {
			case <-wCh: // The listern has returned
				break angryPackets
			default:
				_, _ = l.conn.WriteTo(l.angryPacket, l.Props.SrcAddr)
			}
		}
		_ = l.conn.Close()
	}
	return cancelAndWait, nil
}
