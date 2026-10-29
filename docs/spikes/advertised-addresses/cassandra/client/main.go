// Command cqlclient runs a small workload through gocql with its default discovery, then prints every
// address the driver dialed, one per line, including connections it later closed; a dial that failed
// is printed as "ADDR failed".
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gocql/gocql"
)

// recorder is a gocql dialer that remembers each dial and whether it failed.
type recorder struct {
	mu    sync.Mutex
	addrs []string
	net.Dialer
}

func (r *recorder) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := r.Dialer.DialContext(ctx, network, addr)
	if err != nil {
		addr += " failed"
	}
	r.mu.Lock()
	r.addrs = append(r.addrs, addr)
	r.mu.Unlock()
	return conn, err
}

func main() {
	dials := &recorder{Dialer: net.Dialer{Timeout: 10 * time.Second}}
	c := gocql.NewCluster(os.Args[1:]...)
	c.Timeout = 10 * time.Second
	c.Dialer = dials
	s, err := c.CreateSession()
	must(err)
	defer s.Close()
	must(s.Query(`CREATE KEYSPACE IF NOT EXISTS shop WITH replication = {'class':'SimpleStrategy','replication_factor':1}`).Exec())
	must(s.Query(`CREATE TABLE IF NOT EXISTS shop.orders (id int PRIMARY KEY)`).Exec())
	for i := range 100 {
		must(s.Query(`INSERT INTO shop.orders (id) VALUES (?)`, i).Exec())
	}
	time.Sleep(2 * time.Second) // let the pools finish dialing every host they discovered
	dials.mu.Lock()
	defer dials.mu.Unlock()
	for _, a := range dials.addrs {
		fmt.Println(a)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
