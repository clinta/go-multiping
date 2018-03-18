package listener

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/net/icmp"

	"github.com/TrilliumIT/go-multiping/internal/listenMap/internal/messages"
	"github.com/TrilliumIT/go-multiping/internal/listenMap/internal/process"
	"github.com/TrilliumIT/go-multiping/ping"
)

type Listener struct {
	proto int
	l     sync.RWMutex
	dead  chan struct{}
	wg    sync.WaitGroup
	conn  *icmp.PacketConn
	Props *messages.Props
}

func New(p int) *Listener {
	l := &Listener{proto: p}
	switch p {
	case 4:
		l.Props = messages.V4Props
	case 6:
		l.Props = messages.V6Props
	}
	l.dead = make(chan struct{})
	close(l.dead)
	return l
}

func (l *Listener) Running() bool {
	l.l.RLock()
	r := l.usRunning()
	l.l.RUnlock()
	return r
}

type ErrNotRunning struct{}

func (e *ErrNotRunning) Error() string {
	return "listener not running"
}

func (l *Listener) Send(p *ping.Ping, dst net.Addr) error {
	l.l.RLock()
	defer l.l.RUnlock()
	if !l.usRunning() {
		return &ErrNotRunning{}
	}
	l.wg.Add(1)
	defer l.wg.Done()
	// TODO the rest of the send
	p.Sent = time.Now()
	b, err := p.ToICMPMsg()
	if err != nil {
		return err
	}
	p.Len, err = l.conn.WriteTo(b, dst)
	return nil
}

func (l *Listener) usRunning() bool {
	select {
	case <-l.dead:
		return false
	default:
	}
	return true
}

func (l *Listener) WgAdd(delta int) {
	l.l.RLock()
	l.wg.Add(delta)
	l.l.RUnlock()
}

func (l *Listener) WgDone() {
	l.wg.Done()
}

func (l *Listener) Run(getCb func(net.IP, uint16) func(context.Context, *ping.Ping)) error {
	l.l.Lock()
	defer l.l.Unlock()
	if l.usRunning() {
		return nil
	}

	l.dead = make(chan struct{})
	var err error
	l.conn, err = icmp.ListenPacket(l.Props.Network, l.Props.Src)
	if err != nil {
		return err
	}
	err = setPacketCon(l.conn)
	if err != nil {
		_ = l.conn.Close()
		return err
	}

	// this is not inheriting a context. Each ip has a context, which will decrement the waitgroup when it's done.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			l.wg.Wait()
			l.l.Lock()
			wgC := make(chan struct{})
			go func() { l.wg.Wait(); close(wgC) }()
			select {
			case <-wgC:
			default:
				l.l.Unlock()
				continue
			}
			_ = l.conn.Close()
			cancel()
			<-l.dead
			l.l.Unlock()
		}
	}()

	go func() {
		defer close(l.dead)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			r := &messages.RecvMsg{
				Payload: make([]byte, l.Props.ExpectedLen),
			}
			err := readPacket(l.conn, r)
			if err != nil {
				continue
			}
			r.Recieved = time.Now()
			go process.ProcessMessage(ctx, r, getCb)
		}
	}()
	return nil
}
