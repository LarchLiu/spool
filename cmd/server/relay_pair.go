package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"spool/internal/workspace"
	"spool/relay"
)

// The command is local console administration, never a network approval route.
func approveRelayPhone(ctx context.Context, service *workspace.Service) error {
	return approveRelayPhoneIO(ctx, service, os.Stdin, os.Stdout)
}

func approveRelayPhoneIO(ctx context.Context, service *workspace.Service, input io.Reader, out io.Writer) error {
	code, err := service.RelayPairingCode()
	if err != nil {
		return err
	}
	approved := false
	defer func() {
		if !approved {
			_ = service.CancelRelayPairing(code)
		}
	}()
	fmt.Fprintln(out, "Paste this one-device invitation into Spool Android within five minutes:")
	fmt.Fprintln(out, code)
	deadline := time.NewTimer(relay.PairingLifetime)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("pairing invitation expired; run 'spool-server relay pair' again")
		case <-ticker.C:
			snapshot, err := service.RelayDevices()
			if err != nil {
				return err
			}
			if len(snapshot.Pending) == 0 {
				continue
			}
			device := snapshot.Pending[0]
			fmt.Fprintf(out, "Phone: %s\nDevice fingerprint: %s\nCompare with the phone. Type yes to approve: ", device.Name, device.ID)
			answer := make(chan string, 1)
			go func() { line, _ := bufio.NewReader(input).ReadString('\n'); answer <- strings.TrimSpace(line) }()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return fmt.Errorf("pairing invitation expired")
			case line := <-answer:
				approve := line == "yes"
				if err := service.DecideRelayDevice(device.RequestID, approve); err != nil {
					return err
				}
				if !approve {
					return fmt.Errorf("pairing rejected")
				}
				// Leave this consumed invitation available until expiry so the
				// approved key can recover a lost response. Other keys cannot use it.
				approved = true
				return nil
			}
		}
	}
}
