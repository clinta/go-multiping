package pinger

import (
	"context"
	"sync"

	"github.com/TrilliumIT/go-multiping/ping"
	"github.com/TrilliumIT/go-multiping/pinger/internal/pending"
)

type procPing struct {
	p   *pending.Ping
	ctx context.Context
}

// returns the proto func and a worker waitgroup which should be waited on before canceling ctx
func getProcFunc(ctx context.Context, workers, buffer int, m *pending.Map, h func(*ping.Ping, error), pktWg *sync.WaitGroup) (func(*procPing), func()) {
	// start workers
	wWg := sync.WaitGroup{}
	if workers < -1 {
		return func(p *procPing) {
			p.p.Wait(p.ctx, m, h)
		}, func() {}
	}

	if workers == 0 {
		return func(p *procPing) {
			pktWg.Add(1)
			go func() {
				p.p.Wait(p.ctx, m, h)
				pktWg.Done()
			}()
		}, func() {}
	}

	pCh := make(chan *procPing, buffer)
	if workers == -1 {
		return func(p *procPing) {
			wWg.Add(1)
			select {
			case <-ctx.Done():
				wWg.Done()
				return
			case pCh <- p:
				return
			default:
			}
			pktWg.Add(1)
			go func() {
				runWorker(ctx, pCh, m, h, wWg.Done)
				pktWg.Done()
			}()
			select {
			case <-ctx.Done():
				wWg.Done()
				return
			case pCh <- p:
			}
		}, wWg.Wait
	}

	for w := 0; w < workers; w++ {
		pktWg.Add(1)
		go func() {
			runWorker(ctx, pCh, m, h, wWg.Done)
			pktWg.Done()
		}()
	}

	return func(p *procPing) {
		wWg.Add(1)
		select {
		case <-ctx.Done():
			wWg.Done()
			return
		case pCh <- p:
		}
	}, wWg.Wait
}

func runWorker(ctx context.Context, pCh <-chan *procPing, m *pending.Map, h func(*ping.Ping, error), done func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-pCh:
			p.p.Wait(p.ctx, m, h)
			done()
		}
	}
}
