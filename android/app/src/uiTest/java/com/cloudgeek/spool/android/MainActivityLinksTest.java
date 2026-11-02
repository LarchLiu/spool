package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.content.Intent;
import android.text.Spanned;
import android.text.style.ClickableSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowDialog;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityLinksTest {
    private void open(MainActivityGoalTest.Harness h, String target) {
        ReflectionHelpers.callInstanceMethod(h.activity, "openMessageLink", ReflectionHelpers.ClassParameter.from(String.class, target));
    }

    private void external(MainActivityGoalTest.Harness h, String target) {
        ReflectionHelpers.callInstanceMethod(h.activity, "openExternalLink", ReflectionHelpers.ClassParameter.from(String.class, target));
    }

    private void handedOff(MainActivityGoalTest.Harness h, String address) {
        Intent intent = Shadows.shadowOf(h.activity).getNextStartedActivity(); assertNotNull(address, intent);
        assertEquals(Intent.ACTION_VIEW, intent.getAction()); assertTrue(intent.hasCategory(Intent.CATEGORY_BROWSABLE));
        assertEquals(android.net.Uri.parse(address).normalizeScheme(), intent.getData());
        assertNull("One click must dispatch exactly once", Shadows.shadowOf(h.activity).getNextStartedActivity());
    }

    private TextView paragraph(View view) {
        if (view instanceof TextView) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            TextView found = paragraph(((ViewGroup) view).getChildAt(i)); if (found != null) return found;
        }
        return null;
    }

    @Test public void userLinkClicksOpenImmediatelyWithoutAnExtraNativeDialogOrGoRequest() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            int dialogs = ShadowDialog.getShownDialogs().size();
            for (String address : new String[] {"https://example.com/a%2Fb?q=a%26b", "HTTP://example.org/a", "mailto:a+b@example.org?subject=Hello%20there",
                    "irc://irc.example.org/channel", "ircs://irc.example.org/channel", "xmpp:person@example.com"}) {
                open(h, address); handedOff(h, address);
                external(h, address); handedOff(h, address);
                assertEquals("The original Web anchor has no confirmation modal", dialogs, ShadowDialog.getShownDialogs().size());
            }
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void actualWebLinkVectorsReachTheSameHandoffThroughProductionMarkdownSpans() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-markdown-links.json")) {
            assertNotNull(input); cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("cases");
        }
        int clicks = 0;
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            int dialogs = ShadowDialog.getShownDialogs().size();
            for (int i = 0; i < cases.length(); i++) {
                JSONObject item = cases.getJSONObject(i);
                View markdown = NativeMarkdown.view(h.get("ui"), item.getString("source"), false, address -> open(h, address));
                assertNull("Rendering must not open a browser", Shadows.shadowOf(h.activity).getNextStartedActivity());
                TextView view = paragraph(markdown); assertNotNull(view);
                Spanned document = (Spanned) view.getText();
                List<ClickableSpan> spans = new ArrayList<>(java.util.Arrays.asList(document.getSpans(0, document.length(), ClickableSpan.class)));
                spans.sort(Comparator.comparingInt(document::getSpanStart).thenComparingInt(document::getSpanEnd));
                JSONArray links = item.getJSONArray("actionable"); assertEquals(links.length(), spans.size());
                for (int j = 0; j < links.length(); j++) {
                    JSONObject link = links.getJSONObject(j);
                    String address = link.getString("target");
                    if (!NativeExternalLink.allowed(address)) continue;
                    JSONObject anchor = null;
                    JSONArray anchors = item.getJSONArray("anchors");
                    for (int k = 0; k < anchors.length(); k++) {
                        JSONObject candidate = anchors.getJSONObject(k);
                        if (candidate.getInt("start") == link.getInt("start") && candidate.getInt("end") == link.getInt("end")) anchor = candidate;
                    }
                    assertNotNull("The real Web renderer must produce an external anchor", anchor);
                    assertEquals(address, anchor.getString("href")); assertEquals("_blank", anchor.getString("target"));
                    assertEquals("noopener noreferrer", anchor.getString("rel"));
                    spans.get(j).onClick(view); handedOff(h, address); clicks++;
                }
                assertEquals(dialogs, ShadowDialog.getShownDialogs().size());
            }
            assertTrue("Exercise the real Web external-link fixtures", clicks > 25);
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void unsafeLinksAndInactiveOrRevokedWorkspacesNeverLaunchAnotherApp() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            for (String address : new String[] {"javascript:alert(1)", "javascript%3Aalert(1)", "data:text/html,x", "intent://example.com", "tel:123",
                    "https://example.com/%0a", "mailto:a@example.com?body=%0dInjected", "https://user:secret@example.com", "#missing-heading"}) {
                open(h, address); external(h, address); assertNull(Shadows.shadowOf(h.activity).getNextStartedActivity());
            }
            h.controller.pause().stop();
            open(h, "https://background.example.org"); external(h, "https://background.example.org");
            assertNull(Shadows.shadowOf(h.activity).getNextStartedActivity());
            h.set("client", null); h.controller.start().resume();
            open(h, "https://foreground.example.org"); handedOff(h, "https://foreground.example.org");
            h.call("clearWorkspace");
            open(h, "https://revoked.example.org"); external(h, "https://revoked.example.org");
            assertNull(Shadows.shadowOf(h.activity).getNextStartedActivity());
            h.set("user", new JSONObject().put("id", "alice")); h.set("destroyed", true);
            open(h, "https://destroyed.example.org"); external(h, "https://destroyed.example.org");
            assertNull(Shadows.shadowOf(h.activity).getNextStartedActivity());
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void readOnlyAndPostingStateDoNotDisableUserLinksOrAlterTheCurrentDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.composer().input.setText("Unsent draft"); h.composer().input.setSelection(2, 7);
            Object document = h.composer().input.getText();
            h.<JSONObject>get("chat").put("can_control", false); h.set("busy", true);
            open(h, "https://example.org"); handedOff(h, "https://example.org");
            assertSame(document, h.composer().input.getText()); assertEquals("Unsent draft", document.toString());
            assertEquals(2, h.composer().input.getSelectionStart()); assertEquals(7, h.composer().input.getSelectionEnd());
            assertTrue(h.<Boolean>get("busy")); assertEquals("chat-one", h.<String>get("chatID"));
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }
}
