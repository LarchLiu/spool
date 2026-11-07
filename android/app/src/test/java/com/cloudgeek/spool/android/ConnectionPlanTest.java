package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class ConnectionPlanTest {
    @Test public void migrationKeepsThePreviousAddressInItsOwnNetworkEntry() {
        for (ConnectionMode mode : ConnectionMode.values()) {
            ConnectionPlan plan = ConnectionPlan.restore(null, null, "", "https://old.example.com", mode.value);
            assertEquals("https://old.example.com", plan.origin(mode));
            assertEquals(1, plan.ordered(null).size());
        }
    }
    @Test public void selectionAndOptionalEntriesSurviveNormalization() {
        ConnectionPlan plan = new ConnectionPlan(" https://CHAT.example.com:443/ ", "https://spool.example.ts.net", ConnectionMode.TAILSCALE, true).validated();
        assertEquals("https://chat.example.com", plan.direct);
        assertEquals(ConnectionMode.TAILSCALE, plan.candidates(null).get(0).mode);
        assertEquals(2, plan.candidates(null).size());
        assertTrue(plan.autoSwitch);
        assertEquals(ConnectionMode.DIRECT, new ConnectionPlan(plan.direct, "", ConnectionMode.TAILSCALE).ordered(null).get(0).mode);
    }
}
