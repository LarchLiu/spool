package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class ConnectionModeTest {
    @Test public void newInstallsDefaultToDirectAndExistingServersRetainTailscale() {
        assertSame(ConnectionMode.DIRECT, ConnectionMode.restore("", ""));
        assertSame(ConnectionMode.TAILSCALE, ConnectionMode.restore("", "https://spool.example.ts.net"));
        assertSame(ConnectionMode.TAILSCALE, ConnectionMode.restore("unknown", "https://spool.example.ts.net"));
    }

    @Test public void explicitModesSurviveRestartAndKeepTheirSessionsSeparate() {
        assertSame(ConnectionMode.DIRECT, ConnectionMode.restore("direct", "https://chat.example.com"));
        assertSame(ConnectionMode.TAILSCALE, ConnectionMode.restore("tailscale", ""));
        assertNotEquals(ConnectionMode.DIRECT.directory, ConnectionMode.TAILSCALE.directory);
        assertEquals("tsnet", ConnectionMode.TAILSCALE.directory);
    }
}
