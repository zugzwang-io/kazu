// Command cqlclient runs a small workload through gocql with its default discovery, then prints the
// remote address of every TCP connection it holds, one per line.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gocql/gocql"
)

func main() {
	c := gocql.NewCluster(os.Args[1:]...)
	c.Timeout = 10 * time.Second
	c.ConnectTimeout = 10 * time.Second
	s, err := c.CreateSession()
	must(err)
	defer s.Close()
	must(s.Query(`CREATE KEYSPACE IF NOT EXISTS shop WITH replication = {'class':'SimpleStrategy','replication_factor':1}`).Exec())
	must(s.Query(`CREATE TABLE IF NOT EXISTS shop.orders (id int PRIMARY KEY)`).Exec())
	for i := range 100 {
		must(s.Query(`INSERT INTO shop.orders (id) VALUES (?)`, i).Exec())
	}
	tcp, err := os.ReadFile("/proc/net/tcp")
	must(err)
	for _, line := range strings.Split(string(tcp), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) > 3 && f[3] == "01" { // ESTABLISHED
			fmt.Println(remote(f[2]))
		}
	}
}

// remote decodes /proc/net/tcp's little-endian hex "0B004D0A:2352" into "10.77.0.11:9042".
func remote(hex string) string {
	ip, port, _ := strings.Cut(hex, ":")
	b := make([]string, 4)
	for i := range 4 {
		v, _ := strconv.ParseUint(ip[6-2*i:8-2*i], 16, 8)
		b[i] = strconv.FormatUint(v, 10)
	}
	p, _ := strconv.ParseUint(port, 16, 16)
	return strings.Join(b, ".") + ":" + strconv.FormatUint(p, 10)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
