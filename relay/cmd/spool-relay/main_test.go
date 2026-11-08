package main

import (
	"flag"
	"testing"
)

func TestRegistrationTokenConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, environment string
		args              []string
		want              bool
		invalid           bool
	}{
		{name: "secure default", want: true},
		{name: "empty environment", environment: "  ", want: true},
		{name: "automatic registration", environment: "false"},
		{name: "required token", environment: "true", want: true},
		{name: "trim whitespace", environment: " false\n"},
		{name: "invalid environment", environment: "automatic", invalid: true},
		{name: "flag requires token", environment: "false", args: []string{"-require-registration-token=true"}, want: true},
		{name: "flag allows registration", environment: "true", args: []string{"-require-registration-token=false"}},
		{name: "explicit flag overrides invalid environment", environment: "invalid", args: []string{"-require-registration-token=true"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet("spool-relay", flag.ContinueOnError)
			required := flags.Bool("require-registration-token", true, "")
			if err := flags.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			got, err := registrationTokenRequired(flags, *required, tc.environment)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid configuration must prevent startup")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("require token = %t, error = %v; want %t", got, err, tc.want)
			}
		})
	}
}
