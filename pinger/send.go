package pinger

import (
	"math/rand"
	"net"
	"time"

	protoPinger "github.com/TrilliumIT/go-multiping/internal/pinger"
	"github.com/TrilliumIT/go-multiping/packet"
)

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

// Run runs the ping. It blocks until an error is returned or the ping is stopped.
// After calling Stop(), Run will continue to block for timeout to allow the last packet to be returned.
func (d *Dst) Run() error {
	d.sending = make(chan struct{})

	buf := 2 * (d.timeout.Nanoseconds() / d.interval.Nanoseconds())
	if buf < 2 {
		buf = 2
	}
	d.pktCh = make(chan *pkt, buf)
	d.runSend()

	t := make(chan struct{})
	d.cbWg.Add(1)
	go func() {
		defer d.cbWg.Done()
		var rd time.Duration
		if d.randDelay {
			rd = time.Duration(rand.Int63n(d.interval.Nanoseconds()))
		}
		ft := time.NewTimer(rd)
		select {
		case <-ft.C:
			t <- struct{}{}
		case <-d.stop:
			ft.Stop()
			return
		}
		ft.Stop()

		ti := time.NewTicker(d.interval)
		defer close(t)
		defer ti.Stop()
		for {
			select {
			case <-ti.C:
				select {
				case t <- struct{}{}:
					continue
				case <-d.stop:
					return
				case <-d.sending:
					return
				}
			case <-d.stop:
				return
			case <-d.sending:
				return
			}
		}
	}()

	var dst *net.IPAddr
	var pp *protoPinger.Pinger
	var delCallback = func() error { return nil }
	defer func() { _ = delCallback() }()
	count := -1
	id := rand.Intn(1<<16 - 1)
	for range t {
		count++
		if d.count > 0 && count >= d.count {
			break
		}
		sp := &packet.Packet{
			ID:   id,
			Seq:  int(uint16(count)),
			Sent: time.Now(),
		}
		if dst == nil || d.reResolve {
			nDst, nPP, changed, err := d.resolve(dst, pp)
			if err != nil && !d.reResolve {
				return err
			}
			if err != nil {
				err = d.afterSendError(sp, err)
				if err != nil {
					return err
				}
				continue
			}
			if changed {
				err := nPP.AddCallBack(nDst.IP, id, d.afterReply)
				if err != nil {
					if _, ok := err.(*protoPinger.ErrorAlreadyExists); ok {
						// Try a different ID
						id = rand.Intn(1<<16 - 1)
						continue
					}
					return err
				}
				err = delCallback()
				if err != nil {
					return err
				}
				delCallback = func() error {
					return pp.DelCallBack(dst.IP, id)
				}
				dst, pp = nDst, nPP
			}
		}
		sp.Dst = dst.IP
		d.beforeSend(sp)
		err := pp.Send(dst, sp)
		if err != nil {
			// this returns nil if onError is set
			err = d.afterSendError(sp, err)
			if err != nil {
				return err
			}
			continue
		}
		d.afterSend(sp)
	}

	close(d.sending)
	d.cbWg.Wait()
	select {
	case <-d.stop:
	default:
		d.Stop()
	}

	// we didn't early return, we should check err from delCallback instead of letting defer handle it
	err := delCallback()
	// so the defer is okay
	delCallback = func() error { return nil }
	return err
}

func (d *Dst) resolve(dst *net.IPAddr, pp *protoPinger.Pinger) (*net.IPAddr, *protoPinger.Pinger, bool, error) {
	rdst, err := net.ResolveIPAddr("ip", d.host)
	if err != nil {
		return dst, pp, false, err
	}

	if dst != nil && rdst.IP.Equal(dst.IP) {
		return dst, pp, false, nil
	}

	return rdst, d.pinger.getProtoPinger(rdst.IP), true, nil
}
