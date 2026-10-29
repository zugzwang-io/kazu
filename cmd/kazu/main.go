// Command kazu runs resilience regression tests. Its subcommands:
//
//	kazu proxy -listen ADDR -upstream HOST:PORT
//
// proxy forwards every TCP connection on ADDR to HOST:PORT until it is sent
// SIGINT or SIGTERM. Kazu runs one in a container per declared edge.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/zugzwang-io/kazu/proxy"
)

const usage = "usage: kazu proxy -listen ADDR -upstream HOST:PORT"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "kazu:", err)
		os.Exit(1)
	}
}

// run executes the subcommand named by args[0], logging to stderr.
func run(ctx context.Context, args []string, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "proxy":
		return runProxy(ctx, args[1:], stderr)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func runProxy(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "", "address to accept connections on, such as :8081")
	upstream := fs.String("upstream", "", "host:port to forward every connection to")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if *listen == "" || *upstream == "" || fs.NArg() > 0 {
		return errors.New(usage)
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *listen)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	log.Info("proxy listening", "listen", ln.Addr().String(), "upstream", *upstream)
	return proxy.New(*upstream, log).Serve(ctx, ln)
}
