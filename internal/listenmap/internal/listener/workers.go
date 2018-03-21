package listener

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/TrilliumIT/go-multiping/internal/listenmap/internal/messages"
	"github.com/TrilliumIT/go-multiping/ping"
)

// returns a wait function
func (l *Listener) startWorkers(
	ctx context.Context,
	workers, buffer int,
	getCb func(net.IP, uint16) func(context.Context, *ping.Ping),
) func() {

	wWg := sync.WaitGroup{}

	if workers < -1 {
		wWg.Add(1)
		go func() {
			l.singleWorker(ctx, getCb)
			wWg.Done()
		}()
		return wWg.Wait
	}

	if workers == -1 {
		wr := make(chan struct{}, 16)
		wWg.Add(1)
		go func() {
			l.dynamicWorker(ctx, getCb, wr, func() { wWg.Add(1) }, wWg.Done)
			wWg.Done()
		}()
		return wWg.Wait
	}

	if workers == 0 {
		wWg.Add(1)
		go func() {
			l.goRoutineWorker(ctx, getCb, func() { wWg.Add(1) }, wWg.Done)
			wWg.Done()
		}()
		return wWg.Wait
	}

	for w := 0; w < workers; w++ {
		wWg.Add(1)
		go func() {
			l.singleWorker(ctx, getCb)
			wWg.Done()
		}()
	}
	return wWg.Wait
}

func ctxDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
	}
	return false
}

func (l *Listener) readPacket() (*messages.RecvMsg, error) {
	r := &messages.RecvMsg{
		Payload: make([]byte, l.Props.ExpectedLen),
	}
	err := readPacket(l.conn, r)
	r.Recieved = time.Now()
	return r, err
}

func (l *Listener) singleWorker(
	ctx context.Context,
	getCb func(net.IP, uint16) func(context.Context, *ping.Ping),
) {
	for {
		r, err := l.readPacket()
		if ctxDone(ctx) {
			return
		}
		if err != nil {
			continue
		}
		processMessage(ctx, r, getCb)
	}
}

func (l *Listener) goRoutineWorker(
	ctx context.Context,
	getCb func(net.IP, uint16) func(context.Context, *ping.Ping),
	wgAdd func(),
	wgDone func(),
) {
	for {
		r, err := l.readPacket()
		wgAdd()
		go func(r *messages.RecvMsg, err error) {
			defer wgDone()
			if ctxDone(ctx) {
				return
			}
			if err != nil {
				return
			}
			processMessage(ctx, r, getCb)
		}(r, err)

		if ctxDone(ctx) {
			return
		}
	}
}

func (l *Listener) dynamicWorker(
	ctx context.Context,
	getCb func(net.IP, uint16) func(context.Context, *ping.Ping),
	wr chan struct{},
	wgAdd func(),
	wgDone func(),
) {
	for {
		wr <- struct{}{}
		r, err := l.readPacket()
		if ctxDone(ctx) {
			return
		}
		if err != nil {
			continue
		}
		select {
		case <-wr: // something else is ready for packets, leave it alone
			wr <- struct{}{}
		default:
			wgAdd()
			go func() {
				l.dynamicWorker(ctx, getCb, wr, wgAdd, wgDone)
				wgDone()
			}()
		}
		processMessage(ctx, r, getCb)
	}
}

func processMessage(
	ctx context.Context,
	r *messages.RecvMsg,
	getCb func(net.IP, uint16) func(context.Context, *ping.Ping),
) {
	p := r.ToPing()
	if p == nil {
		return
	}

	cb := getCb(p.Dst, uint16(p.ID))
	if cb == nil {
		return
	}

	cb(ctx, p)
}
