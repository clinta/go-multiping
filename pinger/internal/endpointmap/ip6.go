package endpointmap

import (
	"encoding/binary"
	"net"

	"github.com/TrilliumIT/go-multiping/pinger/internal/seqmap"
)

func toIP6Idx(ip net.IP, id int) [18]byte {
	var r [18]byte
	copy(r[0:16], ip.To16())
	binary.LittleEndian.PutUint16(r[16:], uint16(id))
	return r
}

type ip6m map[[18]byte]*seqmap.Map

func (i ip6m) add(ip net.IP, id int, sm *seqmap.Map) {
	i[toIP6Idx(ip, id)] = sm
}

func (i ip6m) del(ip net.IP, id int) {
	delete(i, toIP6Idx(ip, id))
}

func (i ip6m) get(ip net.IP, id int) (*seqmap.Map, bool) {
	sm, ok := i[toIP6Idx(ip, id)]
	return sm, ok
}

func (i ip6m) length() int {
	return len(i)
}