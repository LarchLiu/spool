package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import org.junit.Test;

import java.nio.file.Files;
import java.nio.file.Path;

/** Source-level guardrails for the native migration; device rendering is a separate check. */
public class NativeArchitectureTest {
    private Path root() {
        Path directory = Path.of("").toAbsolutePath();
        while (directory != null && !Files.isDirectory(directory.resolve("android/app/src/main")))
            directory = directory.getParent();
        assertNotNull("Run tests inside the repository", directory);
        return directory;
    }

    private String source() throws Exception {
        return Files.readString(
                root().resolve(
                                "android/app/src/main/java/com/cloudgeek/spool/android/MainActivity.java"));
    }

    @Test
    public void workspaceUsesNativeViewsWithoutBrowserDependencies() throws Exception {
        String source = source();
        assertFalse(source.contains("android.webkit"));
        assertFalse(source.contains("new WebView"));
        assertFalse(source.contains(".loadUrl("));
        assertTrue(source.contains("new NestedScrollView(this)"));
        assertTrue(source.contains("captured.subscribeChat(id, loc,"));
        assertTrue(source.contains("new NativeChatEvents<>"));
        String navigation = source.substring(source.indexOf("private void openChat("), source.indexOf("private void renderChat()"));
        assertTrue(navigation.contains("startChat();"));
        assertFalse("Healthy navigation must not fetch history before opening SSE", navigation.contains("api("));
        assertFalse(
                Files.readString(root().resolve("android/app/build.gradle"))
                        .contains("androidx.webkit"));
    }

    @Test
    public void normalHeaderContainsNoConnectionDiagnostics() throws Exception {
        String source = source();
        int start = source.indexOf("private void workspacePage()");
        String header = source.substring(start, source.indexOf("history = new NestedScrollView", start));
        for (String forbidden :
                new String[] {
                    "origin",
                    "authURL",
                    "connectionInfo",
                    "tailnetState",
                    "connectionMessage",
                    "httpsState"
                }) assertFalse("header exposed " + forbidden, header.contains(forbidden));
        assertTrue(header.contains("projectName()"));
        assertTrue(header.contains("this::openSidebar"));
    }

    @Test
    public void tsnetControlsBelongToSettingsOnly() throws Exception {
        String source = source();
        int start = source.indexOf("private void settingsPage()");
        String settings =
                source.substring(
                        start, source.indexOf("private void saveLocalPreferences()", start));
        assertTrue(settings.contains("new NativeConnectionSettings("));
        assertTrue(settings.contains("serverInput ="));
        assertTrue(settings.contains("OriginPolicy.isLoginURL(authURL)"));
        assertTrue(settings.contains("this::closeSettings"));
    }

    @Test public void httpsRetriesDoNotBlockInterfaceUpdatesOrDisconnectAndDropStaleResults() throws Exception {
        String activity = source();
        String poll = activity.substring(activity.indexOf("private void pollStatus("), activity.indexOf("private void probeHTTPS("));
        assertFalse(poll.contains("captured.probe()"));
        assertTrue(poll.contains("post(token, () -> probeHTTPS(token, captured))"));
        String probe = activity.substring(activity.indexOf("private void probeHTTPS("), activity.indexOf("private void restoreSession("));
        assertTrue(probe.contains("httpsWorker.execute("));
        assertTrue(probe.contains("generation != token || destroyed"));
        assertTrue(probe.contains("post(token,"));
        assertTrue(probe.contains("connectionMessage = \"\""));
        String interfaces = activity.substring(activity.indexOf("private void publishInterfaces()"), activity.indexOf("private void connect("));
        assertTrue(interfaces.contains("connectionWorker.execute("));
        assertFalse(interfaces.contains("httpsWorker"));
        String disconnect = activity.substring(activity.indexOf("private void disconnect()"), activity.indexOf("private void clearWorkspace()"));
        assertTrue(disconnect.contains("++generation"));
        assertTrue(disconnect.contains("connectionWorker.execute("));
        assertTrue(disconnect.contains("captured.close()"));
        assertTrue(activity.contains("httpsWorker.shutdown()"));
    }

    @Test public void settingsReuseTheWebModalAndKeepAutosavesSessionScoped() throws Exception {
        String activity = source();
        int start = activity.indexOf("private void settingsPage()");
        String settings = activity.substring(start, activity.indexOf("private void saveLocalPreferences()", start));
        assertTrue(settings.contains("new NativeSettings("));
        assertTrue(settings.contains("settingsDialog.content.appearance.saving(preferencesSaving)"));
        assertFalse(settings.contains("new AlertDialog"));
        String preferences = activity.substring(activity.indexOf("private void updatePreferences("), activity.indexOf("private void updateConnectionSettings()"));
        assertTrue(preferences.contains("if (preferencesSaving"));
        assertTrue(preferences.contains("session != authEpoch"));
        assertTrue(preferences.contains("appearance.saving(false)"));
    }

    @Test
    public void avatarsGenerateLocallyWithoutNetworkOrAJavaScriptRuntime() throws Exception {
        String dependencies = Files.readString(root().resolve("android/app/build.gradle"));
        assertFalse(dependencies.contains("rhino"));
        assertFalse(Files.exists(root().resolve("android/app/src/main/assets/web-avatar.js")));
        String avatar = Files.readString(root().resolve("android/app/src/main/java/com/cloudgeek/spool/android/NativeAvatars.java"));
        assertTrue(avatar.contains("spoolnet.Spoolnet::renderAvatar"));
        assertTrue(source().contains("new NativeAvatars(imageWorker)"));
        assertFalse(source().contains("loadAvatar("));
        for (String forbidden : new String[] {"/api/", "java.net", "Client", "WebView", ".request("})
            assertFalse("Avatar generation must be independent from transport: " + forbidden, avatar.contains(forbidden));
        String bridge = Files.readString(root().resolve("android/transport/spoolnet/avatar.go"));
        assertTrue(bridge.contains("\"spool/internal/avatar\""));
        assertFalse(bridge.contains("net/http"));
    }

    @Test public void attachmentImagesStayOnGoTransportAndDownloadsRequireAnExplicitDocumentDestination() throws Exception {
        String activity = source();
        assertTrue(activity.contains("NativeFileData.attachmentPath(ownerChat,"));
        assertFalse(activity.contains("file.optString(\"preview_url\")"));
        assertTrue(activity.contains("new NativeImagePreview(ui,"));
        assertTrue(activity.contains("new Intent(Intent.ACTION_CREATE_DOCUMENT)"));
        assertTrue(activity.contains("getContentResolver().openOutputStream(destination, \"wt\")"));
        assertTrue(activity.contains("!\"content\".equals(destination.getScheme())"));
        assertTrue(activity.contains("selected.session != authEpoch"));
        assertTrue(activity.contains("fileImages.clear()"));
        String images = Files.readString(root().resolve("android/app/src/main/java/com/cloudgeek/spool/android/NativeImages.java"));
        assertTrue(images.contains("NativeFileData.allowedPath(path)"));
        assertTrue(images.contains("captured.load(path)"));
        for (String forbidden : new String[] {"java.net.URL", "HttpURLConnection", "okhttp", "openConnection", "WebView"})
            assertFalse(images.contains(forbidden));
    }

    @Test public void projectFilesUseReusableNativePanelsAndSessionScopedGoReads() throws Exception {
        String activity = source();
        String files = activity.substring(activity.indexOf("private void openFiles("), activity.indexOf("private void previewBinary("));
        assertTrue(files.contains("new NativeFileBrowser("));
        assertTrue(files.contains("fileState.project.equals(projectID)"));
        assertTrue(files.contains("Client captured = client"));
        assertTrue(files.contains("token != generation || session != authEpoch"));
        assertTrue(files.contains("request(captured, path.substring(4), null, auth, loc)"));
        assertTrue(files.contains("completion.done(null, error)"));
        assertTrue(files.contains("fileBrowser.select(target)"));
        assertFalse(files.contains("AlertDialog"));
        for (String name : new String[] {"NativeFileBrowser", "NativeFilePreview", "NativeFileSource", "NativeFileSplit"}) {
            String component = Files.readString(root().resolve("android/app/src/main/java/com/cloudgeek/spool/android/" + name + ".java"));
            for (String forbidden : new String[] {"import android.webkit", "new WebView", "java.net.URL", "HttpURLConnection", "openConnection("})
                assertFalse(name + " bypasses native transport: " + forbidden, component.contains(forbidden));
        }
    }

    @Test public void peopleChatUsesNativeFloatingPanelsAndCapturedSessionScopedWebSocketRequests() throws Exception {
        String activity = source();
        String people = activity.substring(activity.indexOf("private void openPeople()"), activity.indexOf("private void openFiles("));
        assertTrue(people.contains("new NativePeoplePanel(")); assertTrue(people.contains("new NativeGroupPicker("));
        assertTrue(people.contains("new NativePeopleMenu("));
        assertFalse(people.contains("AlertDialog"));
        assertTrue(people.contains("EventStream captured = workspaceStream"));
        assertTrue(people.contains("session != authEpoch || streamEpoch != workspaceEpoch"));
        assertTrue(people.contains("captured.send(payload.toString())"));
        assertTrue(people.contains("10000"));
        assertTrue(activity.contains("inbox.acknowledge(message)"));
        assertTrue(activity.contains("inbox.receive(message"));
        assertTrue(activity.contains("case \"group_result\""));
    }

    @Test public void navigationUsesSharedNativeSidebarStateInsteadOfActivityOwnedFlatButtons() throws Exception {
        String activity = source();
        String navigation = activity.substring(activity.indexOf("private void openSidebar()"), activity.indexOf("private void refreshWorkspace()"));
        assertTrue(navigation.contains("new NativeSidebar(")); assertTrue(navigation.contains("sidebar.bind(")); assertTrue(navigation.contains("sidebarState.select(")); assertTrue(navigation.contains("sidebar.deactivate()"));
        assertFalse(navigation.contains("navigationButton(")); assertFalse(navigation.contains("new AlertDialog")); assertTrue(activity.contains("sidebarState.reset()"));
        for (String component : new String[] {"NativeSidebar", "NativeSidebarState", "NativeChatLink", "NativeChatOperators", "NativeProjectRow"}) {
            String text = Files.readString(root().resolve("android/app/src/main/java/com/cloudgeek/spool/android/" + component + ".java"));
            for (String forbidden : new String[] {"android.webkit", "new WebView", "java.net.URL", "HttpURLConnection", "openConnection("}) assertFalse(component + " must remain a reusable native view", text.contains(forbidden));
        }
    }

    @Test public void selectableSourceMarkdownAndDiffsReuseNativeTabLayoutInsteadOfReplacingContents() throws Exception {
        for (String component : new String[] {"NativeFileSource", "NativeMarkdown", "NativePatchView"}) {
            String text = Files.readString(root().resolve("android/app/src/main/java/com/cloudgeek/spool/android/" + component + ".java"));
            assertTrue(component + " must share the CSS tab layout", text.contains("NativeCodeTabs."));
            assertFalse(component + " must preserve copied tab characters", text.contains("replace(\"\\t\""));
        }
    }

    @Test public void animatedImagesShareNativeVisibilityLifecycleAndNeverAddANetworkOrBrowserDecoder() throws Exception {
        String prefix = "android/app/src/main/java/com/cloudgeek/spool/android/";
        for (String name : new String[] {"NativeImages", "NativeImageSequence", "NativeAnimatedImage", "NativeImageView"}) {
            String component = Files.readString(root().resolve(prefix + name + ".java"));
            for (String forbidden : new String[] {"android.webkit", "new WebView", "java.net.URL", "HttpURLConnection", "openConnection(", "loadUrl("}) assertFalse(name + " bypasses native transport", component.contains(forbidden));
        }
        assertTrue(Files.readString(root().resolve(prefix + "NativeContainedImage.java")).contains("extends NativeImageView"));
        assertTrue(Files.readString(root().resolve(prefix + "NativeAttachmentCard.java")).contains("new NativeImageView("));
        String loader = Files.readString(root().resolve(prefix + "NativeImages.java")); assertTrue(loader.contains("animation.release(true)")); assertTrue(loader.contains("token != epoch"));
        assertTrue(source().contains("imageWorker.shutdown()"));
    }

    @Test public void syntaxUsesNativeSharedAdaptersAndDrainsSessionOwnedResourcesBeforeWorkerShutdown() throws Exception {
        String prefix = "android/app/src/main/java/com/cloudgeek/spool/android/";
        for (String name : new String[] {"NativeSyntax", "NativeSyntaxEngine"}) {
            String component = Files.readString(root().resolve(prefix + name + ".java"));
            for (String forbidden : new String[] {"android.webkit", "new WebView", "java.net.URL", "HttpURLConnection", "openConnection(", "loadUrl("})
                assertFalse(name + " must remain native/offline", component.contains(forbidden));
        }
        String activity = source(); assertTrue(activity.contains("syntax.clear()"));
        assertTrue(activity.indexOf("syntax.close()") < activity.indexOf("codeWorker.shutdown()"));
        String module = Files.readString(root().resolve("android/textmate/build.gradle"));
        assertFalse(module.contains("org.jruby.joni")); assertFalse(module.contains("sora-editor"));
        String appearance = Files.readString(root().resolve(prefix + "NativeAppearanceSection.java"));
        assertTrue(appearance.contains("new String[] {\"system\", \"zh-CN\", \"en\", \"ja\"}"));
    }
}
