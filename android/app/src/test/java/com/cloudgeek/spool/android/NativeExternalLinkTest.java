package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class NativeExternalLinkTest {
    @Test public void onlyUserHandoffProtocolsAreAllowedWithoutChangingTheApiOriginPolicy() {
        for (String safe : new String[] {"https://example.com/a%2Fb?q=a%26b", "HTTP://example.org/a", "http://a_b.example.com",
                "https://\u4f8b\u5b50.\u6d4b\u8bd5/a", "mailto:person+tag@example.com?subject=Hello%20there",
                "irc://irc.example.org/channel", "ircs://irc.example.org/channel", "xmpp:person@example.com"})
            assertTrue(safe, NativeExternalLink.allowed(safe));
        OriginPolicy api = new OriginPolicy("https://spool.example.ts.net");
        assertFalse(api.matches("http://spool.example.ts.net"));
        assertFalse(api.matches("https://a_b.example.com"));
        assertFalse(api.matches("mailto:person@example.com"));
        assertFalse(api.matches("https://other.example.com"));
    }

    @Test public void unsafeSchemesControlsCredentialsAndMalformedEscapesNeverReachAnIntent() {
        for (String unsafe : new String[] {"", "#section", "//example.com", "file:///private/a", "javascript:alert(1)",
                "javascript%3Aalert(1)", "data:text/html,x", "intent://example.com", "tel:123", "https://user:secret@example.com",
                "https://user%40example.com", "https://example.com/%00", "https://example.com/%0a", "mailto:a@example.org?body=%0dInjected",
                "https://example.com/%7f", "https://example.com/%FF", "https://example.com/%E0%A4", "https://example.com/%XZ",
                "mailto:", "https://", "https://exa mple.com"}) assertFalse(unsafe, NativeExternalLink.allowed(unsafe));
        assertTrue(NativeExternalLink.inline("#section"));
        assertTrue(NativeExternalLink.inline("src/main.tsx#L12C3"));
        assertFalse(NativeExternalLink.inline("javascript%3Aalert(1)"));
    }
}
