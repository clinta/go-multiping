package pinger

import (
	"sync"
	"time"

	"github.com/clinta/go-multiping/packet"
)

func wrapCallbacks(
	onReply func(*packet.Packet),
	onSend func(*packet.SentPacket),
	onSendError func(*packet.SentPacket, error),
	onTimeout func(*packet.SentPacket),
	stop <-chan struct{},
	sending <-chan struct{},
	timeout time.Duration,
	interval time.Duration,
) (
	func(*packet.Packet), // onReply
	func(*packet.SentPacket), // onSend
	func(*packet.SentPacket, error), // onSendError
	func(), // wait func
) {
	wg := sync.WaitGroup{}
	buf := 2 * (timeout.Nanoseconds() / interval.Nanoseconds())
	if buf < 2 {
		buf = 2
	}
	pktCh := make(chan *pkt, buf)
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTimer(timeout)
		if !t.Stop() {
			<-t.C
		}
		pending := make(map[uint16]*packet.SentPacket)
		for {
			select {
			case p := <-pktCh:
				processPkt(pending, p, t, timeout, onTimeout)
				continue
			case <-stop:
				return
			default:
			}

			select {
			case <-sending:
				if len(pending) == 0 {
					return
				}
			default:
			}

			select {
			case p := <-pktCh:
				processPkt(pending, p, t, timeout, onTimeout)
				continue
			case n := <-t.C:
				processTimeout(pending, t, timeout, onTimeout, n, &wg)
			case <-stop:
				return
			}
		}
	}()

	rOnSend := func(p *packet.SentPacket) {
		if onSend != nil {
			wg.Add(1)
			go func() {
				onSend(p)
				wg.Done()
			}()
		}
		pktCh <- &pkt{sent: p}
	}

	var rOnSendError func(*packet.SentPacket, error)

	if onSendError != nil {
		rOnSendError = func(p *packet.SentPacket, err error) {
			if onSendError != nil {
				wg.Add(1)
				go func() {
					onSendError(p, err)
					wg.Done()
				}()
			}
			pktCh <- &pkt{err: p}
		}
	}

	rOnReply := func(p *packet.Packet) {
		if p.Sent.Add(timeout).Before(p.Recieved) {
			if onTimeout != nil {
				wg.Add(1)
				go func() {
					onTimeout(p.ToSentPacket())
					wg.Done()
				}()
			}
		} else {
			if onReply != nil {
				wg.Add(1)
				go func() {
					onReply(p)
					wg.Done()
				}()
			}
		}
		pktCh <- &pkt{recv: p}
	}

	//return onReply, rOnSend, rOnSendError
	return rOnReply, rOnSend, rOnSendError, wg.Wait
}

type pkt struct {
	sent *packet.SentPacket
	recv *packet.Packet
	err  *packet.SentPacket
}

func processPkt(pending map[uint16]*packet.SentPacket, p *pkt, t *time.Timer, timeout time.Duration, onTimeout func(*packet.SentPacket)) {
	if p.sent != nil {
		pending[uint16(p.sent.Seq)] = p.sent
		if len(pending) == 1 {
			resetTimer(t, p.sent.Sent, timeout)
		}
	}
	if p.recv != nil {
		delete(pending, uint16(p.recv.Seq))
	}
	if p.err != nil {
		delete(pending, uint16(p.err.Seq))
	}
	if len(pending) == 0 {
		stopTimer(t)
	}
}

func resetTimer(t *time.Timer, s time.Time, d time.Duration) {
	stopTimer(t)
	rd := time.Until(s.Add(d))
	if rd < time.Nanosecond {
		rd = time.Nanosecond
	}
	t.Reset(rd)
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func processTimeout(pending map[uint16]*packet.SentPacket, t *time.Timer, timeout time.Duration, onTimeout func(*packet.SentPacket), n time.Time, wg *sync.WaitGroup) {
	var resetS time.Time
	for s, p := range pending {
		if p.Sent.Add(timeout).Before(n) {
			if onTimeout != nil {
				wg.Add(1)
				go func(p *packet.SentPacket) {
					defer wg.Done()
					onTimeout(p)
				}(p)
			}
			delete(pending, s)
			continue
		}
		if resetS.IsZero() || resetS.After(p.Sent) {
			resetS = p.Sent
		}
	}

	if !resetS.IsZero() {
		resetTimer(t, resetS, timeout)
	}
}
