package pinger

import (
	"math/rand"
	"net"
	"time"

	protoPinger "github.com/clinta/go-multiping/internal/pinger"
	"github.com/clinta/go-multiping/packet"
)

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

// Run runs the ping. It blocks until an error is returned or the ping is stopped.
// After calling Stop(), Run will continue to block for timeout to allow the last packet to be returned.
func (d *Dst) Run() error {
	e, m := protoPinger.NewEcho()

	onReply, onSend, onSendError := wrapCallbacks(
		d.onReply, d.onSend, d.onSendError, d.onTimeout,
		d.stop, d.timeout, d.interval)

	t := make(chan struct{})
	go func() {
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
		defer ti.Stop()
		for {
			select {
			case <-ti.C:
				t <- struct{}{}
			case <-d.stop:
				close(t)
				return
			}
		}
	}()

	var dst *net.IPAddr
	var pp *protoPinger.Pinger
	var delCallback = func() error { return nil }
	defer func() { _ = delCallback() }()
	count := 0
	for range t {
		if dst == nil || d.onResolveError != nil {
			nDst, nPP, changed, err := d.resolve(dst, pp)
			if err != nil && d.onResolveError == nil {
				return err
			}
			if err != nil {
				d.onResolveError(&packet.SentPacket{
					ID:   e.ID,
					Seq:  e.Seq,
					Sent: time.Now(),
				}, err)
				continue
			}
			if changed {
				err := nPP.AddCallBack(nDst.IP, e.ID, onReply)
				if err != nil {
					if _, ok := err.(*protoPinger.ErrorAlreadyExists); ok {
						// Try a different ID
						e.ID = rand.Intn(1<<16 - 1)
						continue
					}
					return err
				}
				err = delCallback()
				if err != nil {
					return err
				}
				delCallback = func() error {
					return pp.DelCallBack(dst.IP, e.ID)
				}
				m.Type = nPP.SendType()
				dst, pp = nDst, nPP
			}
		}
		err := pp.Send(dst, m, onSend, onSendError)
		if err != nil {
			return err
		}
		e.Seq = int(uint16(e.Seq) + 1)
		count++
		if d.count > 0 && count >= d.count {
			time.Sleep(d.timeout)
			break
		}
	}

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
