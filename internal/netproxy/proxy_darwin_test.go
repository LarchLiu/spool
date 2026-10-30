package netproxy

import (
	"reflect"
	"testing"
)

func TestMacSystemProxyStaticConfig(t *testing.T) {
	config, err := parseMacSystemProxy(`<dictionary> {
  HTTPEnable : 1
  HTTPProxy : 127.0.0.1
  HTTPPort : 8889
  HTTPSEnable : 1
  HTTPSProxy : ::1
  HTTPSPort : 8889
  ExcludeSimpleHostnames : 1
  ExceptionsList : <array> {
    0 : *.local
    1 : 10.*
    2 : 172.2*
  }
  __SCOPED__ : <dictionary> {
    ignored : <dictionary> {
      HTTPProxy : wrong
    }
  }
}`)
	if err != nil {
		t.Fatal(err)
	}
	if config.http != "http://127.0.0.1:8889" || config.https != "http://[::1]:8889" || !config.excludeSimple || !reflect.DeepEqual(config.exceptions, []string{"*.local", "10.*", "172.2*"}) {
		t.Fatalf("%+v", config)
	}
}

func TestMacSystemProxyRejectsUnsupportedOrInvalidConfig(t *testing.T) {
	for _, value := range []string{
		"ProxyAutoConfigEnable : 1", "ProxyAutoDiscoveryEnable : 1", "SOCKSEnable : 1",
		"HTTPEnable : 1\nHTTPProxy : proxy\nHTTPPort : 65536", "HTTPSEnable : 1\nHTTPSPort : 443",
	} {
		if _, err := parseMacSystemProxy("<dictionary> {\n" + value + "\n}"); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	if config, err := parseMacSystemProxy("<dictionary> {\nHTTPEnable : 0\nSOCKSEnable : 0\n}"); err != nil || config.http != "" || config.https != "" {
		t.Fatal(config, err)
	}
}
