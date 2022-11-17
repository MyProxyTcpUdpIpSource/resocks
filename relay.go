package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/armon/go-socks5"
	"github.com/hashicorp/yamux"
	"github.com/spf13/cobra"
)

func relayCommand() *cobra.Command {
	timeout := 5 * time.Second
	reconnectAfter := time.Duration(0)
	connectionKey := fromEnvWithFallback(ConnectionKeyEnvVariable, defaultConnectionKey)
	insecure := false

	relayCmd := &cobra.Command{
		Use:           fmt.Sprintf("%s <connect back address> --key <connection key>", binaryName()),
		Short:         fmt.Sprintf("Connect back to an %s listener and relay the SOCKS5 traffic", binaryName()),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemoteProxyRelay(
				withDefaultPort(args[0], DefaultListenPort), // connect back address
				connectionKey,
				timeout,
				reconnectAfter,
				insecure,
			)
		},
	}

	flags := relayCmd.Flags()
	flags.DurationVar(&timeout, "timeout", timeout, "Connect back timeout")
	flags.DurationVar(&reconnectAfter, "reconnect-after", reconnectAfter,
		"Enables reconnect after given duration")
	flags.StringVarP(&connectionKey, "key", "k", connectionKey,
		"Connection key that is displayed when starting a listener")
	flags.BoolVar(&insecure, "insecure", insecure,
		"Don't check server certificate and only send client certificate when a connection key is specified")

	return relayCmd
}

func runRemoteProxyRelay(connectBackAddr string, connectionKey string, timeout time.Duration,
	reconnectAfter time.Duration, insecure bool,
) error {
	tlsConfig, err := clientTLSConfig(connectionKey, insecure)
	if err != nil {
		return fmt.Errorf("build TLS config: %w", err)
	}

	for {
		err := connectBackAndRelay(tlsConfig, connectBackAddr, timeout)
		if err != nil {
			if reconnectAfter == 0 {
				return err
			}

			fmt.Printf("error: %v\n", err)
		}

		if reconnectAfter == 0 {
			return nil
		}

		fmt.Printf("reconnecting after %v\n", reconnectAfter)

		time.Sleep(reconnectAfter)
	}
}

func connectBackAndRelay(tlsConfig *tls.Config, connectBackAddr string, timeout time.Duration) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", connectBackAddr, tlsConfig)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	fmt.Printf("connected to %s\n", conn.RemoteAddr())

	defer conn.Close() //nolint:errcheck

	yamuxServer, err := yamux.Server(conn, yamuxCfg())
	if err != nil {
		return fmt.Errorf("initialize multiplexer: %w", err)
	}

	defer yamuxServer.Close() //nolint:errcheck

	socksServer, err := socks5.New(&socks5.Config{})
	if err != nil {
		return fmt.Errorf("initialize socks5 server: %w", err)
	}

	err = socksServer.Serve(yamuxServer)
	if err != nil {
		return fmt.Errorf("socks5 server: %w", err)
	}

	return nil
}

func clientTLSConfig(connectionKey string, insecure bool) (*tls.Config, error) {
	switch {
	default:
		key, err := ParseConnectionKey(connectionKey)
		if err != nil {
			return nil, fmt.Errorf("parse connection key: %w", err)
		}

		cfg, err := ClientTLSConfig(key)
		if err != nil {
			return nil, fmt.Errorf("configure TLS: %w", err)
		}

		return cfg, nil

	case !insecure && connectionKey == "": // in secure mode a connection key is required
		return nil, fmt.Errorf("connection key is required")
	case insecure && connectionKey == "": // don't send client cert and don't check server cert
		return &tls.Config{InsecureSkipVerify: true}, nil //nolint:gosec
	case insecure && connectionKey != "": // send client cert but don't check server cert
		key, err := ParseConnectionKey(connectionKey)
		if err != nil {
			return nil, fmt.Errorf("parse connection key: %w", err)
		}

		cfg, err := ClientTLSConfig(key)
		if err != nil {
			return nil, fmt.Errorf("configure TLS: %w", err)
		}

		cfg.InsecureSkipVerify = true
		cfg.ServerName = ""

		return cfg, nil
	}
}
