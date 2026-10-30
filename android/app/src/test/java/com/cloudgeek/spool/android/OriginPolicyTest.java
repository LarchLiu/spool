package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class OriginPolicyTest {
    @Test public void normalizesHTTPSOrigin() {
        OriginPolicy policy = new OriginPolicy(" https://SPOOL.example.ts.net:443/ ");
        assertEquals("https://spool.example.ts.net", policy.origin);
        assertTrue(policy.matches("https://spool.example.ts.net/api/me"));
        assertTrue(policy.matches("https://spool.example.ts.net:443/chat?id=1"));
        assertFalse(policy.matches("http://spool.example.ts.net/"));
        assertFalse(policy.matches("https://spool.example.ts.net:444/"));
        assertFalse(policy.matches("https://spool.example.ts.net.attacker.test/"));
        assertFalse(policy.matches("https://spool.example.ts.net@attacker.test/"));
        assertFalse(policy.matches("https://user@spool.example.ts.net/"));
        assertFalse(policy.allowsResource("file:///data/data/secrets"));
        assertFalse(policy.allowsResource("content://private/document"));
        assertTrue(policy.allowsResource("blob:https://spool.example.ts.net/example"));
        assertFalse(policy.allowsResource("blob:https://other.example.ts.net/example"));
    }

    @Test public void rejectsNonOriginsAndLocalAddresses() {
        for (String value : new String[] {
                "http://spool.example.ts.net", "https://spool.example.ts.net/chat",
                "https://spool.example.ts.net?", "https://spool.example.ts.net#",
                "https://user:secret@spool.example.ts.net", "https://localhost",
                "https://127.0.0.1", "https://[::1]", "https://spool.example.ts.net:65536"}) {
            try {
                new OriginPolicy(value);
                fail("accepted " + value);
            } catch (IllegalArgumentException expected) { }
        }
    }

    @Test public void loginOnlyUsesOfficialHTTPSHost() {
        assertTrue(OriginPolicy.isLoginURL("https://login.tailscale.com/a/example"));
        assertFalse(OriginPolicy.isLoginURL("http://login.tailscale.com/a/example"));
        assertFalse(OriginPolicy.isLoginURL("https://login.tailscale.com.attacker.test/a/example"));
        assertFalse(OriginPolicy.isLoginURL("https://secret@login.tailscale.com/a/example"));
        assertFalse(OriginPolicy.isExternalWebURL("intent://arbitrary"));
        assertFalse(OriginPolicy.isExternalWebURL("javascript:alert(1)"));
    }
}
