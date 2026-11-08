package com.cloudgeek.spool.android;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.ClipData;
import android.content.Intent;
import android.content.res.Configuration;
import android.database.Cursor;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.text.InputType;
import android.text.Editable;
import android.text.TextWatcher;
import android.text.TextUtils;
import android.text.method.LinkMovementMethod;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import androidx.core.widget.NestedScrollView;
import android.widget.TextView;
import android.widget.Toast;

import go.Seq;

import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

import spoolnet.Client;
import spoolnet.EventStream;
import spoolnet.Spoolnet;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.text.DateFormat;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** All workspace views are native. Only explicit external links use a browser. */
public final class MainActivity extends Activity {
    private static final int PICK_FILES = 10;
    private static final int SAVE_FILE = 11;
    private NativeAppUpdate appUpdates;
    private NativeUpdateView updateView;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final ExecutorService io = Executors.newFixedThreadPool(3);
    private final ExecutorService workspaceWorker = Executors.newFixedThreadPool(3);
    private final ExecutorService workspaceSocketWorker = Executors.newFixedThreadPool(2);
    private final ExecutorService workspaceProbeWorker = Executors.newSingleThreadExecutor();
    private final ExecutorService streamCloser = Executors.newSingleThreadExecutor();
    private final ExecutorService payloadWorker = Executors.newSingleThreadExecutor();
    private java.util.concurrent.Executor workspaceDecode = payloadWorker;
    private final ExecutorService connectionWorker = Executors.newSingleThreadExecutor();
    private final ExecutorService httpsWorker = Executors.newSingleThreadExecutor();
    private final ExecutorService diagnostics = Executors.newSingleThreadExecutor();
    private final ExecutorService imageWorker = Executors.newFixedThreadPool(2);
    private final ExecutorService codeWorker = Executors.newSingleThreadExecutor();
    private final NativeSyntax syntax = new NativeSyntax(codeWorker, new NativeSyntaxEngine(path -> new java.io.InputStreamReader(
            getAssets().open("syntax/" + path), java.nio.charset.StandardCharsets.UTF_8)));
    private final NativeAvatars avatarImages = new NativeAvatars(imageWorker);
    private final NativeImages fileImages = new NativeImages(imageWorker, () -> {
        Client captured = this.client;
        return path -> loadBinary(captured, path);
    });
    private final ConnectionMonitor monitor =
            new ConnectionMonitor(
                    diagnostics,
                    new ConnectionMonitor.Dispatcher() {
                        public void post(Runnable r) {
                            main.post(r);
                        }

                        public void postDelayed(Runnable r, long delay) {
                            main.postDelayed(r, delay);
                        }

                        public void remove(Runnable r) {
                            main.removeCallbacks(r);
                        }
                    });
    private final ConnectionRecovery recovery = new ConnectionRecovery(httpsWorker, new ConnectionRecovery.Scheduler() {
        public void post(Runnable task) { main.post(task); }
        public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
        public void remove(Runnable task) { main.removeCallbacks(task); }
        public long now() { return android.os.SystemClock.elapsedRealtime(); }
    });
    private boolean sessionCheckBusy;
    private int sessionCheckGeneration, sessionCheckAuth;
    private boolean connectionWanted;
    private int connectionStartAttempts;
    private final Runnable connectionRetry = this::retryConnectionStart;
    private volatile int generation;
    private volatile boolean destroyed;
    private volatile int authEpoch;
    private volatile int workspaceEpoch;
    private int selectionEpoch, draftVersion;
    private boolean foreground, connected, connecting, settingsOpen, busy, workspaceReady, restoringSession;
    private boolean presenceStale;
    private String presenceScope = "";
    private final NativePerformanceProbe performanceProbe = new NativePerformanceProbe();
    private volatile Client client;
    private EventStream workspaceStream;
    private NativeWorkspaceEvents<EventStream> workspaceEvents;
    private NativeChatEvents<JSONObject> chatEvents;
    private ConnectivityManager connectivity;
    private ConnectivityManager.NetworkCallback networkCallback;
    private NativeUi ui;
    private FrameLayout root, drawer;
    private LinearLayout historyContent;
    private NativeKeyedViews transcriptViews;
    private NativeTranscriptViewport transcriptViewport;
    private NativeComposerDock composerDock;
    private NativeSidebar sidebar;
    private NativeConnecting connectingView;
    private boolean sidebarPageDeferred;
    private NativeAttachmentCards fileBar;
    private NestedScrollView history;
    private EditText composer, serverInput, username, password;
    private NativeUi.IconButton send;
    private NativeUi.ChoiceButton modelButton, permissionButton;
    private NativeUi.Composer composerView;
    private NativeUi.Header workspaceHeader;
    private NativeAgentModelMenu modelMenu;
    private NativePopover modelPopover;
    private NativePermissionMenu permissionMenu;
    private boolean permissionLoading, permissionFailed;
    private int permissionRequest;
    private NativeSettings settingsDialog, appearanceDialog;
    private NativeDesktopDialog desktopDialog;
    private boolean desktopAvailable;
    private NativeConnectionSettings connectionSettings;
    private NativeServerSettings serverSettings;
    private NativeServerDialog serverDialog;
    private NativeFileBrowser fileBrowser;
    private NativeFileModel fileState;
    private Button loginSubmit;
    private TextView heading, subheading, connectionText, loginError, connectionHint;
    private JSONObject user, chat, connectionInfo, permissionCatalog;
    private JSONArray projects = new JSONArray(), chats = new JSONArray(), agents = new JSONArray();
    private JSONArray profiles = new JSONArray(),
            presence = new JSONArray(),
            groups = new JSONArray();
    private final Map<String, Draft> drafts = new HashMap<>();
    private final Map<String, NativeActivityList.State> activityStates = new java.util.LinkedHashMap<>() {
        @Override protected boolean removeEldestEntry(Map.Entry<String, NativeActivityList.State> entry) { return size() > 200; }
    };
    private final NativePeopleModel inbox = new NativePeopleModel();
    private final Map<String, NativeGroupPicker.Completion> pendingGroups = new java.util.LinkedHashMap<>();
    private final NativeSidebarState sidebarState = new NativeSidebarState();
    private Draft pickerDraft;
    private String csrf = "", origin = "", authURL = "", connectionMessage = "", networkMessage = "";
    private String tailnetState = "Disconnected", httpsState = "Not checked";
    private ConnectionMode connectionMode = ConnectionMode.DIRECT;
    private ConnectionPlan connectionPlan = new ConnectionPlan("", "", ConnectionMode.DIRECT), connectionDraft = connectionPlan;
    private ConnectionRouter<Client> connectionRouter;
    private ServerProfiles serverProfiles = new ServerProfiles(connectionPlan);
    private static final class ServerRuntime {
        ConnectionRouter<Client> router;
        ConnectionRouter.Candidate<Client> candidate;
        ServerFeed feed;
        boolean selecting;
        final Map<ConnectionMode, String> states = new java.util.EnumMap<>(ConnectionMode.class);
        final Map<ConnectionPlan.Endpoint, ConnectionRecovery.Snapshot> health = new HashMap<>();
    }
    private final Map<String, ServerRuntime> serverRuntimes = new HashMap<>();
    private final Runnable serverRefresh = this::refreshServerConnections;
    private static final class ServerWorkspace {
        String userID = "", projectID = "", chatID = "", backend = "codex", model = "", effort = "", permission = "";
        JSONObject chat;
        JSONArray projects = new JSONArray(), chats = new JSONArray(), agents = new JSONArray(), profiles = new JSONArray();
        boolean agentsInitialized, cached;
        final NativeSidebarState sidebar = new NativeSidebarState();
        final NativeNavigation navigation = new NativeNavigation();
        final NativeProfiles directory = new NativeProfiles();
        final NativeHistoryScroll.State history = new NativeHistoryScroll.State();
        final Map<String, NativeActivityList.State> activityStates = new HashMap<>();
        final Map<String, Draft> drafts = new HashMap<>();
    }
    private static final class ServerFeed {
        final Client client;
        final String userID, csrf, locale;
        final ServerWorkspace cache;
        NativeWorkspaceEvents<EventStream> events;
        NativeWorkspaceData data;
        EventStream stream;
        boolean desktopAvailable;
        JSONArray presence = new JSONArray();
        ServerFeed(Client client, String userID, String csrf, String locale, ServerWorkspace cache) {
            this.client = client; this.userID = userID; this.csrf = csrf; this.locale = locale; this.cache = cache;
        }
    }
    private final Map<String, ServerWorkspace> serverWorkspaces = new HashMap<>();
    private String workspaceUserID = "";
    private boolean workspaceCached;
    private final Map<ConnectionMode, String> routeStates = new java.util.EnumMap<>(ConnectionMode.class);
    private boolean routeSelecting, routeLoginRequired;
    private int routeFailures;
    private long nextRouteProbe;
    private String projectID = "",
            chatID = "",
            backend = "codex",
            model = "",
            effort = "",
            permission = "";
    private String theme = "system", language = "system", panel = "";
    private String loginErrorText = "";
    private String goalNotice = "", goalError = "";
    private NativeSubmission<Attachment> pendingSend;
    private NativeChatRefresh<JSONObject> chatRefresh;
    private NativeWorkspaceData workspaceData;
    private boolean agentsInitialized;
    // Failed/partially acknowledged optimistic turns still own local attachment bytes until replaced.
    private NativeSubmission<Attachment> optimisticSend;
    private final java.util.Set<String> stoppingTurns = new java.util.HashSet<>();
    private NativeHistoryLoader<JSONObject> historyLoader;
    private NativeHistoryScroll historyScroll;
    private final NativeHistoryScroll.State historyScrollState = new NativeHistoryScroll.State();
    private NativePeoplePanel peoplePanel;
    private NativePeopleMenu peopleMenu;
    private NativeGroupPicker groupPicker;
    private NativeFloatingWindow.Bounds peopleBounds;
    private NativeAvatarPicker avatarDialog;
    private NativeImagePreview imagePreview;
    private PendingDownload pendingDownload;
    private boolean downloading;
    private int downloadRequest;
    private final NativeProfiles profileCache = new NativeProfiles();
    private final NativeNavigation navigation = new NativeNavigation();

    private static final class Attachment {
        final String id = UUID.randomUUID().toString(), name, excerpt;
        final byte[] data;
        final NativeFileData file;

        Attachment(String name, String mediaType, byte[] data) throws IOException {
            this.name = name;
            this.data = data;
            this.file = new NativeFileData(mediaType, data);
            this.excerpt = NativeFileData.excerpt(name, file.mediaType, data);
        }
    }

    private static final class PendingDownload {
        final NativeFileData file;
        final int session;
        PendingDownload(NativeFileData file, int session) { this.file = file; this.session = session; }
    }

    private static final class Draft {
        String text = "";
        final List<Attachment> files = new ArrayList<>();
    }

    private interface Result {
        void accept(Object value) throws Exception;
    }

    private static final class ApiFailure extends IOException {
        final int status;

        ApiFailure(int status, String message) {
            super(message);
            this.status = status;
        }
    }

    @Override
    public void onCreate(Bundle state) {
        super.onCreate(state);
        Seq.setContext(getApplicationContext());
        appUpdates = new NativeAppUpdate(this, this::bindAppUpdates);
        theme = getPreferences(MODE_PRIVATE).getString("theme", "system");
        language = getPreferences(MODE_PRIVATE).getString("language", "system");
        origin = getPreferences(MODE_PRIVATE).getString("serverOrigin", "");
        connectionMode = ConnectionMode.restore(getPreferences(MODE_PRIVATE).getString("connectionMode", ""), origin);
        connectionPlan = ConnectionPlan.restore(getPreferences(MODE_PRIVATE).getString("directOrigin", null),
                getPreferences(MODE_PRIVATE).getString("tailscaleOrigin", null),
                getPreferences(MODE_PRIVATE).getString("selectedNetwork", connectionMode.value), origin, connectionMode.value).automatic(getPreferences(MODE_PRIVATE).getBoolean("automaticConnections", true));
        serverProfiles = ServerProfiles.restore(getPreferences(MODE_PRIVATE).getString("servers", null),
                getPreferences(MODE_PRIVATE).getString("selectedServer", "legacy"), connectionPlan);
        connectionPlan = serverProfiles.active() == null ? new ConnectionPlan("", "", ConnectionMode.DIRECT) : serverProfiles.active().plan;
        connectionDraft = connectionPlan;
        origin = "";
        if (!connectionPlan.ordered(null).isEmpty()) {
            ConnectionPlan.Endpoint first = connectionPlan.ordered(null).get(0);
            origin = first.origin; connectionMode = first.mode;
        }
        // Resolve the private, origin-scoped Go session before exposing the login form.
        restoringSession = !origin.isEmpty();
        connectivity = getSystemService(ConnectivityManager.class);
        networkCallback =
                new ConnectivityManager.NetworkCallback() {
                    public void onAvailable(Network network) {
                        publishInterfaces();
                        checkConnectionsNow();
                        resumeWorkspace(true);
                    }

                    public void onLost(Network network) {
                        publishInterfaces();
                        checkConnectionsNow();
                    }

                    public void onLinkPropertiesChanged(Network network, LinkProperties p) {
                        publishInterfaces();
                        checkConnectionsNow();
                    }
                };
        connectivity.registerDefaultNetworkCallback(networkCallback, main);
        if (Build.VERSION.SDK_INT >= 33)
            getOnBackInvokedDispatcher().registerOnBackInvokedCallback(0, this::back);
        panel = "sidebar";
        render();
        if (!origin.isEmpty() && serverProfiles.active().enabled) selectConnections(connectionPlan.selected, false);
    }

    private String tr(String en, String zh) {
        return locale().startsWith("zh") ? zh : NativeText.translate(en, locale());
    }

    @Override public void dump(String prefix, java.io.FileDescriptor fd, java.io.PrintWriter writer, String[] args) {
        if ((getApplicationInfo().flags & android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) == 0
                || args == null || args.length == 0 || !args[0].equals("spool-performance")) {
            super.dump(prefix, fd, writer, args); return;
        }
        // The random probe token is per Activity, not a session ID or credential.
        // dumpsys visits background instances too: only the exact focused page may gesture.
        boolean focused = hasWindowFocus();
        boolean historyVisible = history != null && history.isShown() && !settingsOpen && peoplePanel == null;
        Integer velocity = performanceProbe.fling(args, foreground && focused && historyVisible && !destroyed, selectionEpoch);
        if (velocity != null) history.fling(velocity);
        // Debug-only A/B switch for offscreen preparation; "viewport" reports its state.
        if (args.length >= 3 && args[1].equals("prepare")) NativeTranscriptViewport.prepareEnabled = !args[2].equals("off");
        int rows = 0, mounted = 0;
        if (historyContent != null) for (int i = 0; i < historyContent.getChildCount(); i++) {
            View child = historyContent.getChildAt(i);
            if (child instanceof NativeTranscriptViewport.Row) {
                rows++; if (((NativeTranscriptViewport.Row) child).mounted()) mounted++;
            }
        }
        Runtime runtime = Runtime.getRuntime();
        writer.println("SPOOL_PERF " + object("probeVersion", 2, "probeToken", performanceProbe.token, "selectionEpoch", selectionEpoch,
                "foreground", foreground, "focused", focused, "historyVisible", historyVisible, "flingAccepted", velocity != null,
                "historyHasMore", chat != null && chat.optJSONObject("history") != null && chat.optJSONObject("history").optBoolean("has_more"),
                "turns", array(chat, "turns").length(), "parts", array(chat == null ? null : chat.optJSONObject("transcript"), "parts").length(),
                "views", diagnosticViewCount(root), "virtualRows", rows, "mountedRows", mounted,
                "viewport", transcriptViewport == null ? new JSONObject() : transcriptViewport.metrics(),
                "scrollAnchor", historyScroll == null ? new JSONObject() : historyScroll.metrics(),
                "historyHeight", historyContent == null ? 0 : historyContent.getHeight(), "scrollY", history == null ? 0 : history.getScrollY(),
                "realtime", object("epoch", workspaceEpoch, "connected", workspaceReady, "people", presence.length(),
                        "avatarViews", workspaceHeader == null || workspaceHeader.online.getChildCount() == 0 ? 0
                                : System.identityHashCode(workspaceHeader.online.getChildAt(0))),
                "javaUsedBytes", runtime.totalMemory() - runtime.freeMemory(), "javaMaxBytes", runtime.maxMemory(),
                "toolDetails", ui == null || ui.toolDetails == null ? new JSONObject() : ui.toolDetails.metrics(),
                "textOrigin", NativeTextView.diagnostics(),
                "nativeAllocatedBytes", android.os.Debug.getNativeHeapAllocatedSize()));
    }

    private static int diagnosticViewCount(View view) {
        if (view == null) return 0;
        int count = 1;
        if (view instanceof android.view.ViewGroup) {
            android.view.ViewGroup group = (android.view.ViewGroup) view;
            for (int i = 0; i < group.getChildCount(); i++) count += diagnosticViewCount(group.getChildAt(i));
        }
        return count;
    }

    private String locale() {
        android.os.LocaleList locales = getResources().getConfiguration().getLocales();
        String[] preferred = new String[locales.size()];
        for (int i = 0; i < locales.size(); i++) preferred[i] = locales.get(i).toLanguageTag();
        return NativePreferences.language(language, preferred);
    }

    private String localizedString(int id, Object... arguments) {
        Configuration config = new Configuration(getResources().getConfiguration());
        config.setLocale(Locale.forLanguageTag(locale()));
        return createConfigurationContext(config).getString(id, arguments);
    }

    private int dp(float n) {
        return ui.dp(n);
    }

    private JSONObject object(Object... values) {
        JSONObject result = new JSONObject();
        try {
            for (int i = 0; i < values.length; i += 2)
                result.put((String) values[i], values[i + 1]);
        } catch (Exception e) {
            throw new IllegalArgumentException(e);
        }
        return result;
    }

    private static JSONObject at(JSONArray array, int index) {
        JSONObject value = array.optJSONObject(index);
        return value == null ? new JSONObject() : value;
    }

    private static JSONArray array(JSONObject object, String key) {
        JSONArray value = object == null ? null : object.optJSONArray(key);
        return value == null ? new JSONArray() : value;
    }

    private String selectionKey() {
        return chatID.isEmpty() ? "new/" + projectID : chatID;
    }

    private Draft draft() {
        return drafts.computeIfAbsent(selectionKey(), key -> new Draft());
    }

    private void captureDraft() {
        if (composer != null) draft().text = composer.getText().toString();
    }

    private void discardComposerDrafts() {
        // Web resets the composer on navigation, not on first-send ID adoption or a rerender.
        composer = null;
        pickerDraft = null;
        for (Draft value : drafts.values()) { value.text = ""; value.files.clear(); }
        drafts.clear();
    }

    private void post(int token, Runnable task) {
        main.post(
                () -> {
                    if (!destroyed && generation == token) task.run();
                });
    }

    private void toast(Throwable e) {
        String text = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
        Toast.makeText(this, text.length() > 400 ? text.substring(0, 400) : text, Toast.LENGTH_LONG)
                .show();
    }

    private Object decodeResponse(String raw) throws Exception {
        JSONObject envelope = new JSONObject(raw);
        int code = envelope.optInt("status");
        Object value;
        try {
            value = new JSONTokener(envelope.optString("body")).nextValue();
        } catch (Exception e) {
            throw new ApiFailure(code, tr("The server returned an invalid response", "服务器返回了无效响应"));
        }
        if (code < 200 || code >= 300)
            throw new ApiFailure(
                    code,
                    value instanceof JSONObject
                            ? ((JSONObject) value).optString("error", "HTTP " + code)
                            : "HTTP " + code);
        return value;
    }

    private Object request(Client captured, String path, JSONObject body, String auth, String loc)
            throws Exception {
        if (captured == null)
            throw new IOException(tr("Configure a server in Settings first.", "请先在设置中配置服务器。"));
        final String raw;
        try {
            raw = captured.request(
                        body == null ? "GET" : "POST",
                        "/api" + path,
                        body == null ? "" : body.toString(),
                        auth,
                        loc);
            return decodeResponse(raw);
        } catch (Exception error) {
            if (!(error instanceof ApiFailure) || ((ApiFailure) error).status < 400 || ((ApiFailure) error).status >= 500)
                main.post(() -> connectionFailed(captured));
            throw error;
        }
    }

    private void connectionFailed(Client captured) {
        if (destroyed || !foreground) return;
        if (client == captured) { recovery.suspect(); return; }
        for (ServerProfiles.Server server : serverProfiles.entries) {
            ServerRuntime runtime = serverRuntimes.get(server.id);
            if (runtime != null && runtime.candidate != null && runtime.candidate.client == captured) {
                probeServer(server, true); return;
            }
        }
    }

    private void checkConnectionsNow() {
        recovery.checkNow();
        refreshServerConnections(true);
    }

    private void api(String path, JSONObject body, Result done) {
        api(path, body, done, this::toast);
    }

    private void api(String path, JSONObject body, Result done, java.util.function.Consumer<Exception> failed) {
        if (routeLoginRequired) { failed.accept(new IOException(tr("Sign in to this connection first.", "请先登录此连接。"))); return; }
        final Client captured = client;
        final int token = generation, session = authEpoch;
        final String auth = csrf, loc = locale();
        io.execute(
                () -> {
                    try {
                        if (token != generation || session != authEpoch || destroyed) return;
                        Object value = request(captured, path, body, auth, loc);
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    try {
                                        done.accept(value);
                                    } catch (Exception e) {
                                        toast(e);
                                    }
                                });
                    } catch (Exception e) {
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    if (e instanceof ApiFailure && ((ApiFailure) e).status == 401)
                                        expire();
                                    else failed.accept(e);
                                });
                    }
                });
    }

    private void render() {
        rememberAccount();
        boolean reopenFiles = panel.equals("files");
        boolean reopenSidebar = panel.equals("sidebar");
        captureDraft();
        boolean dark = theme.equals("dark") || theme.equals("system")
                && (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
        // A server tab changes the visible sidebar, not the hidden transcript.
        // Keep its window, tab strip and controls mounted until the user opens a page.
        if (sidebarPageDeferred && reopenSidebar && sidebar != null && ui.dark == dark && ui.locale.equals(locale())) {
            renderNavigation();
            if (settingsOpen) settingsPage();
            monitor.setActive(foreground && settingsOpen);
            if (!routeLoginRequired) {
                if (workspaceData != null) bindWorkspaceData(false);
                if (workspaceEvents != null) startWorkspace();
            }
            return;
        }
        if (ui != null) ui.dismissMenus();
        closeAvatarPicker();
        closeImagePreview();
        detachPeople();
        if (sidebar != null) sidebar.deactivate();
        sidebar = null;
        if (fileBrowser != null) fileBrowser.deactivate();
        fileBrowser = null;
        ui = new NativeUi(this, dark, locale());
        ui.avatars = avatarImages;
        ui.people = this::person;
        ui.codeWorker = codeWorker;
        ui.syntax = syntax;
        final Client detailClient = client;
        final int detailGeneration = generation, detailAuth = authEpoch;
        final String detailCSRF = csrf, detailLocale = locale();
        ui.toolDetails = new NativeToolDetails(io, task -> main.post(task), path -> {
            if (detailGeneration != generation || detailAuth != authEpoch || destroyed) throw new IOException("Session changed");
            return (JSONObject) request(detailClient, path, null, detailCSRF, detailLocale);
        });
        setTheme(dark ? R.style.SpoolDarkTheme : R.style.SpoolTheme);
        root = NativeWindow.content(this, ui);
        composer = null;
        composerView = null;
        modelMenu = null;
        modelPopover = null;
        workspaceHeader = null;
        if (transcriptViewport != null) transcriptViewport.dispose();
        transcriptViewport = null;
        if (historyScroll != null) historyScroll.dispose();
        historyScroll = null;
        history = null;
        historyContent = null;
        transcriptViews = null;
        composerDock = null;
        if (!settingsOpen) {
            connectionText = null;
            connectionHint = null;
            connectionSettings = null;
            serverInput = null;
        }
        connectingView = null;
        drawer = null;
        panel = "";
        username = password = null;
        loginError = null;
        loginSubmit = null;
        if (sidebarPageDeferred && reopenSidebar) { /* The selected server's page is built when the drawer closes. */ }
        else if (restoringSession && routeLoginRequired) connectingPage();
        else if (routeLoginRequired) loginPage();
        else if (user == null && restoringSession) connectingPage();
        else if (user == null) loginPage();
        else workspacePage();
        setContentView(root);
        root.requestApplyInsets();
        if (reopenSidebar) openSidebar(false);
        else if (reopenFiles && user != null) openFiles(".");
        if (settingsOpen) settingsPage();
        if (appearanceDialog != null) { appearanceDialog.bind(ui, theme, language, body -> {}); bindAppUpdates(); }
        if (!settingsOpen && !routeLoginRequired && user != null && !inbox.active.isEmpty()) openPeople(inbox.active);
        monitor.setActive(foreground && settingsOpen);
        if (routeLoginRequired) return;
        if (workspaceData != null) bindWorkspaceData(false);
        if (workspaceEvents != null) startWorkspace();
        if (chatEvents != null) startChat();
    }

    private LinearLayout padded(int horizontal, int vertical) {
        return ui.padded(horizontal, vertical);
    }

    private void gap(LinearLayout parent, int size) {
        parent.addView(ui.space(size));
    }

    private LinearLayout toolbar(String title, Runnable back) {
        LinearLayout row = ui.toolbar(title, back);
        row.getChildAt(0).setContentDescription(tr("Back", "返回"));
        return row;
    }

    private void openSettings() {
        if (serverProfiles.active() == null) { editServer(true); return; }
        if (settingsOpen) return;
        closeAppearance();
        captureDraft();
        ui.dismissMenus();
        closePeople();
        settingsOpen = true;
        settingsPage();
        monitor.setActive(foreground);
    }

    private String appVersion() {
        try {
            return getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (android.content.pm.PackageManager.NameNotFoundException error) {
            throw new IllegalStateException("Cannot read installed app version", error);
        }
    }

    private void openAppearance() {
        if (appearanceDialog != null) return;
        captureDraft(); closePeople();
        appearanceDialog = new NativeSettings(ui, theme, language, appVersion(), this::updatePreferences, this::closeAppearance);
        bindAppUpdates();
        appearanceDialog.show();
    }

    private void bindAppUpdates() {
        if (appearanceDialog == null || appUpdates == null) return;
        LinearLayout container = appearanceDialog.content.updates;
        if (updateView == null || updateView.getParent() != container) {
            updateView = new NativeUpdateView(ui, appUpdates::check, appUpdates::download, () -> appUpdates.install(this), appUpdates::cancel);
            appearanceDialog.content.setUpdateView(updateView);
        }
        updateView.bind(appUpdates);
    }

    private void closeAppearance() {
        if (appearanceDialog == null) return;
        appearanceDialog.dismiss(); appearanceDialog = null;
        updateView = null;
    }

    private void closeSettings() {
        settingsOpen = false;
        dismissSettingsDialog();
        connectionSettings = null;
        serverInput = null;
        connectionText = null;
        connectionHint = null;
        monitor.setActive(false);
    }

    private void dismissSettingsDialog() {
        if (settingsDialog == null) return;
        settingsDialog.dismiss();
        settingsDialog = null;
    }

    private void loginPage() {
        if (serverProfiles.active() == null) {
            LinearLayout page = ui.padded(28, 28); page.setGravity(Gravity.CENTER);
            page.addView(ui.sidebarButton(this::openSidebar));
            page.addView(ui.logo(48)); gap(page, 20);
            page.addView(ui.bold(ui.t("No servers yet."), 20)); gap(page, 16);
            page.addView(ui.button(ui.t("Add server"), true, () -> editServer(true)));
            root.addView(page, new FrameLayout.LayoutParams(-1, -1));
            return;
        }
        NativeLogin login = new NativeLogin(ui, loginErrorText, busy,
                this::performLogin, this::openSettings);
        username = login.username;
        password = login.password;
        loginError = login.error;
        loginSubmit = login.submit;
        NativeUi.IconButton servers = ui.sidebarButton(this::openSidebar);
        LinearLayout.LayoutParams menu = new LinearLayout.LayoutParams(dp(32), dp(32)); menu.setMarginEnd(dp(10));
        login.brand.addView(servers, 0, menu);
        login.story.setPadding(dp(16), 0, dp(16), dp(25));
        login.brand.setLayoutParams(new LinearLayout.LayoutParams(-1, dp(50) - dp(1)));
        LinearLayout.LayoutParams headline = (LinearLayout.LayoutParams) login.headline.getLayoutParams();
        headline.setMarginStart(dp(12)); headline.setMarginEnd(dp(12)); login.headline.setLayoutParams(headline);
        FrameLayout.LayoutParams preferences = (FrameLayout.LayoutParams) login.preferences.getLayoutParams();
        preferences.topMargin = (dp(50) - dp(1) - dp(32)) / 2; preferences.setMarginEnd(dp(16));
        login.preferences.setLayoutParams(preferences);
        root.addView(login, new FrameLayout.LayoutParams(-1, -1));
    }

    private void connectingPage() {
        NativeConnecting page = new NativeConnecting(ui, connectionMessage, this::retrySession, this::openSettings);
        connectingView = page;
        FrameLayout.LayoutParams menu = new FrameLayout.LayoutParams(dp(32), dp(32), Gravity.TOP | Gravity.START);
        menu.topMargin = (dp(50) - dp(1) - dp(32)) / 2; menu.setMarginStart(dp(16));
        page.addView(ui.sidebarButton(this::openSidebar), menu);
        root.addView(page, new FrameLayout.LayoutParams(-1, -1));
    }

    private void updateConnectingPage() {
        if (connectingView != null) connectingView.bind(connectionMessage, connecting || routeSelecting || sessionCheckBusy);
        updateConnectionSettings(); renderNavigation();
    }

    private void retrySession() {
        if (connecting) return;
        if (connected && client != null) {
            connectionMessage = "";
            render();
            restoreSession(client, generation);
        } else connect(origin);
    }

    private void showLoginError(String message) {
        loginErrorText = message == null ? tr("Sign in failed", "登录失败") : message;
        if (loginError != null) {
            loginError.setText(loginErrorText);
            loginError.setVisibility(loginErrorText.isEmpty() ? View.GONE : View.VISIBLE);
        }
    }

    private void performLogin() {
        if (!connected || client == null) {
            showLoginError(tr("Connect to your server in Settings first.", "请先在设置中连接服务器。"));
            return;
        }
        String name = username.getText().toString().trim(), secret = password.getText().toString();
        if (name.isEmpty() || secret.isEmpty()) {
            showLoginError(tr("Enter username and password.", "请输入用户名和密码。"));
            return;
        }
        if (busy) return;
        busy = true;
        loginSubmit.setEnabled(false);
        loginSubmit.setText(ui.t("Signing in…"));
        final int token = generation, session = ++authEpoch;
        final Client captured = client;
        final String loc = locale();
        JSONObject body = object("username", name, "password", secret);
        io.execute(
                () -> {
                    try {
                        JSONObject value = (JSONObject) request(captured, "/login", body, "", loc);
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    busy = false;
                                    JSONObject account = value.optJSONObject("user");
                                    if (routeLoginRequired && user != null && account != null && !user.optString("id").equals(account.optString("id"))) clearWorkspace();
                                    routeLoginRequired = false;
                                    if (connectionRouter != null) connectionRouter.cancel();
                                    routeSelecting = false;
                                    user = account;
                                    csrf = value.optString("csrf");
                                    restoreServerWorkspace();
                                    loginErrorText = "";
                                    render();
                                    refreshWorkspace();
                                    startWorkspace();
                                });
                    } catch (Exception e) {
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    busy = false;
                                    loginErrorText = e.getMessage();
                                    showLoginError(loginErrorText);
                                    if (loginSubmit != null) {
                                        loginSubmit.setEnabled(true);
                                        loginSubmit.setText(tr("Enter workspace", "进入工作空间"));
                                    }
                                });
                    }
                });
    }

    private void publishInterfaces() {
        if (destroyed) return;
        final ConnectionMode mode = connectionMode;
        final List<ConnectionRouter<Client>> routers = new ArrayList<>();
        for (ServerRuntime runtime : serverRuntimes.values()) if (runtime.router != null) routers.add(runtime.router);
        connectionWorker.execute(
                () -> {
                    try {
                        if (mode == ConnectionMode.TAILSCALE || !routers.isEmpty()) Spoolnet.updateInterfaces(NetworkSnapshot.read(connectivity));
                        Client captured = client;
                        if (captured != null) captured.networkChanged();
                        for (ConnectionRouter<Client> router : routers) router.networkChanged();
                    } catch (Exception ignored) {
                        /* A later network callback retries. */
                    }
                });
    }

    private void connect(String address) {
        ConnectionPlan plan = new ConnectionPlan(connectionMode == ConnectionMode.DIRECT ? address : connectionPlan.direct,
                connectionMode == ConnectionMode.TAILSCALE ? address : connectionPlan.tailscale, connectionPlan.selected, connectionPlan.autoSwitch);
        applyConnectionPlan(plan, null);
    }

    private ConnectionRouter<Client> routes() { connectionRouter = routes(serverProfiles.active()); return connectionRouter; }

    private ConnectionRouter<Client> routes(ServerProfiles.Server owner) {
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(owner.id, key -> new ServerRuntime());
        if (runtime.router == null) runtime.router = new ConnectionRouter<>(connectionWorker, task -> main.post(task), new ConnectionRouter.Backend<Client>() {
            public Client open(ConnectionPlan.Endpoint endpoint) throws Exception {
                String directory = owner.directory(getNoBackupFilesDir(), endpoint.mode);
                Client candidate;
                if (endpoint.mode == ConnectionMode.DIRECT) candidate = Spoolnet.newDirectClient(directory, endpoint.origin);
                else {
                    Spoolnet.updateInterfaces(NetworkSnapshot.read(connectivity));
                    candidate = Spoolnet.newClient(directory, endpoint.origin);
                }
                try {
                    candidate.useServerSession(owner.sessionDirectory(getNoBackupFilesDir()), owner.plan.direct, owner.plan.tailscale);
                    return candidate;
                } catch (Exception error) { candidate.close(); throw error; }
            }
            public ConnectionRecovery.Snapshot health(Client candidate) throws Exception {
                JSONObject value = new JSONObject(candidate.health());
                return new ConnectionRecovery.Snapshot(value.optString("state", "Unknown"), value.optString("https", "Not checked"),
                        value.optString("problem"), value.optString("authURL"), value.optString("message"));
            }
            public ConnectionRouter.Session session(Client candidate) throws Exception {
                try {
                    JSONObject value = (JSONObject) request(candidate, "/me", null, "", locale());
                    JSONObject account = value.optJSONObject("user");
                    if (account == null || account.optString("id").isEmpty()) throw new IOException("Invalid session response");
                    return new ConnectionRouter.Session(account.optString("id"), value.toString());
                } catch (ApiFailure error) { if (error.status == 401) return null; throw error; }
            }
            public String login(Client candidate) throws Exception {
                return candidate.loginURL();
            }
            public void networkChanged(Client candidate) { candidate.networkChanged(); }
            public void close(Client candidate) { try { candidate.close(); } catch (Exception ignored) {} }
        });
        return runtime.router;
    }

    private void applyConnectionPlan(ConnectionPlan draft, ConnectionMode preferred) {
        if (serverProfiles.active() == null) return;
        ConnectionPlan validated;
        try {
            for (ConnectionMode mode : ConnectionMode.values()) if (!draft.origin(mode).isEmpty()) {
                try { new OriginPolicy(draft.origin(mode)); }
                catch (Exception error) {
                    if (connectionSettings != null) connectionSettings.input(mode).setError(error.getMessage());
                    return;
                }
            }
            validated = (preferred == null ? draft : draft.select(preferred)).validated();
            if (validated.ordered(preferred).isEmpty()) throw new IllegalArgumentException(tr("Enter at least one HTTPS address.", "请至少填写一个 HTTPS 地址。"));
            if (preferred != null && validated.origin(preferred).isEmpty()) return;
        } catch (Exception error) { connectionMessage = error.getMessage(); updateConnectionSettings(); return; }
        connectionPlan = connectionDraft = validated;
        serverProfiles.active().plan = validated; serverProfiles.active().enabled = true; saveServers();
        getPreferences(MODE_PRIVATE).edit().putString("directOrigin", validated.direct).putString("tailscaleOrigin", validated.tailscale)
                .putString("selectedNetwork", validated.selected.value).putBoolean("automaticConnections", validated.autoSwitch).apply();
        selectConnections(validated.selected, true);
    }

    private void selectConnections(ConnectionMode preferred, boolean manual) {
        if (routeSelecting || destroyed || !manual && (busy || pendingSend != null)) return;
        if (serverProfiles.active() == null || connectionPlan.ordered(preferred).isEmpty() || !serverProfiles.active().enabled) return;
        final ServerProfiles.Server owner = serverProfiles.active();
        final String serverID = owner.id;
        final ConnectionPlan selectedPlan = connectionPlan;
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(serverID, key -> new ServerRuntime());
        runtime.selecting = true;
        connectionWanted = true; routeSelecting = true;
        main.removeCallbacks(connectionRetry);
        final String account = user == null ? "" : user.optString("id");
        if (client == null) {
            connecting = true; restoringSession = true;
            if (connectingView == null) render(); else updateConnectingPage();
        }
        else { updateConnectionSettings(); renderNavigation(); }
        routes().select(connectionPlan.candidates(preferred), account, manual, preferred != null || client == null, new ConnectionRouter.Listener<Client>() {
            public void replacing(Client previous) {
                stopServerFeed(runtime);
                runtime.candidate = null;
                if (!serverProfiles.selected.equals(serverID) || client != previous || destroyed) return;
                beginNetworkHandoff();
            }
            public void sampled(ConnectionPlan.Endpoint endpoint, ConnectionRecovery.Snapshot health, boolean signedIn) {
                if (destroyed) return;
                runtime.health.keySet().retainAll(selectedPlan.ordered(null));
                runtime.health.put(endpoint, health);
                String state = health.healthy() ? tr("Available", "可用")
                        : health.problem.equals("login") ? tr("Tailscale sign-in required", "需要登录 Tailscale")
                        : health.problem.equals("approval") ? tr("Device approval required", "等待设备批准") : tr("Unavailable", "不可用");
                runtime.states.put(endpoint.mode, state);
                if (!serverProfiles.selected.equals(serverID)) return;
                routeStates.put(endpoint.mode, health.healthy() ? tr("Available", "可用")
                        : health.problem.equals("login") ? tr("Tailscale sign-in required", "需要登录 Tailscale")
                        : health.problem.equals("approval") ? tr("Device approval required", "等待设备批准") : tr("Unavailable", "不可用"));
                if (endpoint.mode == ConnectionMode.TAILSCALE && !health.authURL.isEmpty()) authURL = health.authURL;
                updateConnectionSettings();
            }
            public void selected(ConnectionRouter.Candidate<Client> candidate) {
                runtime.selecting = false; runtime.candidate = candidate;
                if (destroyed) return;
                if (!serverProfiles.selected.equals(serverID)) {
                    if (candidate.session != null) try { rememberAccount(owner, new JSONObject(candidate.session.json).optJSONObject("user")); }
                    catch (Exception ignored) {}
                    else if (candidate.sessionChecked) forgetAccount(owner);
                    syncServerFeed(owner); renderNavigation(); return;
                }
                routeSelecting = false; nextRouteProbe = android.os.SystemClock.elapsedRealtime() + 30000;
                adoptConnection(candidate);
            }
            public void unavailable() {
                runtime.selecting = false;
                if (destroyed || !serverProfiles.selected.equals(serverID)) return;
                routeSelecting = false; nextRouteProbe = android.os.SystemClock.elapsedRealtime() + 30000;
                connectionMessage = tr("No other signed-in connection is available. Use an entry below to sign in.", "暂无其他已登录的可用连接，可选择下方入口登录。");
                if (client == null) {
                    connecting = false; busy = false;
                    if (connectingView == null) render(); else updateConnectingPage();
                    if (foreground && connectionWanted) main.postDelayed(connectionRetry, Math.min(30000, 2000L << Math.min(4, connectionStartAttempts++)));
                } else { updateConnectionSettings(); renderNavigation(); }
            }
        });
    }

    private void beginNetworkHandoff() {
        saveServerWorkspace(); closeDesktop(); stopStreams();
        ++generation; ++authEpoch; sessionCheckBusy = false;
        monitor.stop(); recovery.stop();
        client = null; connected = false; connecting = true;
        if (pendingSend != null) {
            goalError = tr("Send status unknown. The message may have arrived; it will not be retried automatically.", "发送状态未知，消息可能已送达，不会自动重发。");
            pendingSend = null;
        }
        busy = true; updateComposer(); updateConnectionSettings(); renderNavigation();
    }

    private void adoptConnection(ConnectionRouter.Candidate<Client> candidate) {
        connectionWanted = true;
        if (client == candidate.client) {
            if (candidate.sessionChecked && candidate.session == null && (user != null || hasWorkspaceCache())) {
                forgetAccount(); stopStreams(); clearWorkspace(); render();
            }
            acceptConnectionHealth(client, generation, candidate.health, candidate.health.healthy());
            updateConnectionSettings(); return;
        }
        saveServerWorkspace(); closeDesktop(); stopStreams();
        final boolean retained = user != null;
        ++generation; ++authEpoch; sessionCheckBusy = false;
        monitor.stop(); recovery.stop();
        client = candidate.client; origin = candidate.endpoint.origin; connectionMode = candidate.endpoint.mode;
        connected = false; connecting = true; connectionInfo = null; routeFailures = 0;
        getPreferences(MODE_PRIVATE).edit().putString("serverOrigin", origin).putString("connectionMode", connectionMode.value).apply();
        if (pendingSend != null) {
            goalError = tr("Send status unknown. The message may have arrived; it will not be retried automatically.", "发送状态未知，消息可能已送达，不会自动重发。");
            pendingSend = null;
        }
        busy = false;
        routeLoginRequired = false;
        if (candidate.sessionChecked && candidate.session == null) { forgetAccount(); clearWorkspaceData(true); }
        if (retained && candidate.session != null && user.optString("id").equals(candidate.session.userID)) {
            try {
                JSONObject value = new JSONObject(candidate.session.json);
                user = value.optJSONObject("user"); csrf = value.optString("csrf");
            } catch (Exception error) { routeLoginRequired = true; csrf = ""; }
            restoringSession = false;
        } else if (retained) {
            // An unavailable network has not rejected the login. Keep API access
            // blocked until health recovers and /me can validate the shared session.
            routeLoginRequired = true; restoringSession = !candidate.sessionChecked; csrf = "";
        }
        else {
            routeLoginRequired = false; restoringSession = true;
            if (candidate.session != null) try {
                JSONObject value = new JSONObject(candidate.session.json);
                user = value.optJSONObject("user"); csrf = value.optString("csrf"); restoringSession = false;
                restoreServerWorkspace();
            } catch (Exception ignored) {}
        }
        final int token = generation;
        if (connectionMode == ConnectionMode.TAILSCALE) monitor.start(client::connectionInfo, snapshot -> {
            if (generation != token) return;
            try { connectionInfo = snapshot == null ? null : new JSONObject(snapshot); } catch (Exception ignored) { connectionInfo = null; }
            updateConnectionSettings();
        });
        render();
        acceptConnectionHealth(client, token, candidate.health, candidate.health.healthy());
        watchConnection(token, client);
        if (user != null && !routeLoginRequired) { refreshWorkspace(); startWorkspace(); startChat(); }
    }

    private void retryConnectionStart() {
        if (foreground && connectionWanted && !destroyed && client == null && !routeSelecting) selectConnections(connectionPlan.autoSwitch ? null : connectionPlan.selected, false);
    }

    private void probeAlternate(ConnectionRecovery.Snapshot health) {
        if (!connectionPlan.autoSwitch || !connectionWanted || routeSelecting || connectionPlan.ordered(null).size() < 2) return;
        if (health.healthy() || health.problem.equals("login") || health.problem.equals("approval")) { routeFailures = 0; return; }
        if (health.problem.equals("starting") && android.os.SystemClock.elapsedRealtime() < nextRouteProbe) return;
        routeFailures++;
        if (routeFailures < 2 || android.os.SystemClock.elapsedRealtime() < nextRouteProbe && routeFailures != 2) return;
        selectConnections(null, false);
    }

    private void enrollTailscale() {
        if (serverProfiles.active() == null) return;
        try {
            String address = connectionDraft.validated().tailscale;
            if (address.isEmpty()) return;
            if (client == null || connectionMode != ConnectionMode.TAILSCALE || !origin.equals(address)) {
                applyConnectionPlan(connectionDraft, ConnectionMode.TAILSCALE); return;
            }
            routes().login(new ConnectionPlan.Endpoint(ConnectionMode.TAILSCALE, address), url -> {
                authURL = url;
                if (OriginPolicy.isLoginURL(url)) openBrowser(url);
                recovery.checkNow();
            }, error -> { connectionMessage = networkMessage = error.getMessage(); updateConnectionSettings(); });
        } catch (Exception error) { connectionMessage = networkMessage = error.getMessage(); updateConnectionSettings(); }
    }

    private void watchConnection(int token, Client captured) {
        final ConnectionMode mode = connectionMode;
        recovery.start(new ConnectionRecovery.Source() {
            public boolean online() { return NetworkSnapshot.online(connectivity, mode); }
            public ConnectionRecovery.Snapshot check() throws Exception {
                JSONObject value = new JSONObject(captured.health());
                return new ConnectionRecovery.Snapshot(value.optString("state", "Unknown"),
                        value.optString("https", "Not checked"), value.optString("problem"),
                        value.optString("authURL"), value.optString("message"));
            }
            public void refreshNetwork() throws Exception {
                if (mode == ConnectionMode.TAILSCALE) Spoolnet.updateInterfaces(NetworkSnapshot.read(connectivity));
                captured.networkChanged();
            }
            public void reconnect() throws Exception { captured.reconnect(); }
            public void login() throws Exception { captured.login(); }
            public long responseAgeMillis() { return captured.responseAgeMillis(); }
            public ConnectionRecovery.Subscription subscribe(Runnable changed) throws Exception {
                if (mode != ConnectionMode.TAILSCALE) return null;
                EventStream subscription = captured.subscribeStatus((kind, data) -> changed.run());
                return subscription == null ? null : subscription::close;
            }
        }, (value, available) -> {
            if (destroyed || generation != token || client != captured) return;
            acceptConnectionHealth(captured, token, value, available);
        });
    }

    private void acceptConnectionHealth(Client captured, int token, ConnectionRecovery.Snapshot value, boolean available) {
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        if (runtime != null) runtime.health.put(new ConnectionPlan.Endpoint(connectionMode, origin), value);
        if (runtime != null && runtime.candidate != null && runtime.candidate.client == captured) {
            ConnectionRouter.Candidate<Client> previous = runtime.candidate;
            runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, captured, value, previous.session, previous.sessionChecked);
        }
        boolean recovered = !connected && available;
        connected = available;
        connecting = !value.healthy() && !value.problem.equals("endpoint");
        tailnetState = value.state;
        httpsState = value.https;
        authURL = value.authURL;
        connectionMessage = value.message == null ? "" : value.message;
        if (value.problem.equals("offline")) connectionMessage = tr("Waiting for network", "等待网络恢复");
        else if (value.problem.equals("network") || value.problem.equals("status") || value.problem.equals("starting"))
            connectionMessage = tr("Reconnecting…", "正在重连…") + (connectionMessage.isEmpty() ? "" : "\n" + connectionMessage);
        networkMessage = connectionMessage;
        updateConnectionSettings();
        renderNavigation();
        if (restoringSession && !value.healthy()) updateConnectingPage();
        probeAlternate(value);
        if (!value.healthy()) {
            if (definitiveConnectionFailure(value)) clearWorkspaceNavigation();
            return;
        }
        if (restoringSession) restoreSession(captured, token);
        else if (routeLoginRequired) return;
        else if (recovered && user != null) {
            resumeWorkspace();
            if (chatEvents != null) chatEvents.stop();
            startChat();
        }
    }

    private void restoreSession(Client captured, int token) {
        final int session = authEpoch;
        if (sessionCheckBusy && sessionCheckGeneration == token && sessionCheckAuth == session) return;
        sessionCheckBusy = true;
        sessionCheckGeneration = token; sessionCheckAuth = session;
        renderNavigation();
        final String loc = locale();
        io.execute(
                () -> {
                    if (token != generation || session != authEpoch || destroyed) return;
                    try {
                        JSONObject result = (JSONObject) request(captured, "/me", null, "", loc);
                        JSONObject account = result.optJSONObject("user");
                        if (account == null || account.optString("id").isEmpty()) throw new IOException("Invalid session response");
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    if (user != null && !user.optString("id").equals(account.optString("id"))) clearWorkspace();
                                    sessionCheckBusy = false;
                                    restoringSession = false; routeLoginRequired = false;
                                    user = account;
                                    csrf = result.optString("csrf");
                                    ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
                                    if (runtime != null && runtime.candidate != null && runtime.candidate.client == captured) {
                                        ConnectionRouter.Candidate<Client> previous = runtime.candidate;
                                        runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, captured, previous.health,
                                                new ConnectionRouter.Session(account.optString("id"), result.toString()));
                                    }
                                    restoreServerWorkspace();
                                    render();
                                    refreshWorkspace();
                                    startWorkspace();
                                });
                    } catch (Exception e) {
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    sessionCheckBusy = false;
                                    if (e instanceof ApiFailure && ((ApiFailure) e).status == 401) {
                                        forgetAccount();
                                        stopStreams(); clearWorkspace();
                                        restoringSession = false;
                                    } else
                                        connectionMessage = e.getMessage();
                                    if (restoringSession && connectingView != null) updateConnectingPage(); else render();
                                });
                    }
                });
    }

    private void connectionFailed(Exception e) {
        connecting = false;
        connectionMessage = networkMessage = e.getMessage();
        if (httpsState.equals("Checking")) httpsState = "Failed";
        clearWorkspaceNavigation();
        if (restoringSession && connectingView != null) updateConnectingPage();
        else if (restoringSession) render();
        else updateConnectionSettings();
    }

    private void disconnect() {
        if (serverProfiles.active() == null) return;
        rememberAccount();
        saveServerWorkspace();
        connectionWanted = false; routeSelecting = false; routeLoginRequired = false;
        serverProfiles.active().enabled = false; saveServers();
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        stopServerFeed(runtime);
        if (runtime != null) { runtime.candidate = null; runtime.selecting = false; }
        main.removeCallbacks(connectionRetry);
        if (connectionRouter != null) connectionRouter.stop();
        ++generation;
        monitor.stop();
        recovery.stop();
        stopStreams();
        clearWorkspace(false);
        restoreServerWorkspace();
        Client captured = client;
        final boolean routed = connectionRouter != null;
        client = null;
        connectionWorker.execute(
                () -> {
                    if (captured != null && !routed)
                        try {
                            captured.close();
                        } catch (Exception ignored) {
                        }
                });
        connected = false;
        connecting = false;
        authURL = "";
        tailnetState = "Disconnected";
        httpsState = "Not checked";
        connectionInfo = null;
        connectionMessage = networkMessage = "";
        settingsOpen = true;
        render();
    }

    private void clearWorkspace() { clearWorkspace(true); }

    private void clearWorkspace(boolean forget) {
        ++authEpoch;
        user = null;
        routeLoginRequired = false;
        restoringSession = false;
        sessionCheckBusy = false;
        csrf = "";
        clearWorkspaceData(forget);
    }

    /** Detach the visible workspace without altering a freshly validated server login. */
    private void clearWorkspaceData(boolean forget) {
        if (forget) serverWorkspaces.remove(serverProfiles.selected);
        workspaceUserID = ""; workspaceCached = false;
        if (sidebar != null && !sidebarPageDeferred) sidebar.deactivate();
        closeDesktop();
        setDesktopAvailable(false);
        if (workspaceData != null) workspaceData.stop();
        agentsInitialized = false;
        if (chatRefresh != null) chatRefresh.stop();
        if (fileBrowser != null) fileBrowser.deactivate();
        fileBrowser = null;
        fileState = null;
        if (forget) avatarImages.clear();
        fileImages.clear();
        syntax.clear();
        closeImagePreview();
        pendingDownload = null;
        downloading = false;
        downloadRequest++;
        ++selectionEpoch;
        chat = null;
        chatID = "";
        projectID = "";
        busy = false;
        pendingSend = null;
        optimisticSend = null;
        stoppingTurns.clear();
        goalNotice = goalError = "";
        discardComposerDrafts();
        activityStates.clear();
        sidebarState.reset();
        inbox.reset();
        pendingGroups.clear();
        peopleBounds = null;
        stopHistory();
        historyScrollState.select("");
        projects = new JSONArray();
        chats = new JSONArray();
        agents = new JSONArray();
        profiles = new JSONArray();
        profileCache.clear();
        navigation.reset();
        presence = new JSONArray();
        presenceScope = "";
        presenceStale = false;
        groups = new JSONArray();
        invalidatePermissions();
        closeAvatarPicker();
        closePeople();
    }

    /** A rejected endpoint clears fetched navigation, while local drafts remain available for retry. */
    private void clearWorkspaceNavigation() {
        if (!workspaceCached && projects.length() == 0 && chats.length() == 0) return;
        stopStreams();
        projects = new JSONArray(); chats = new JSONArray(); workspaceCached = false;
        navigation.reset();
        ServerWorkspace state = serverWorkspaces.get(serverProfiles.selected);
        if (state != null) { state.projects = projects; state.chats = chats; state.cached = false; state.navigation.reset(); }
        updateComposer();
    }

    private boolean definitiveConnectionFailure(ConnectionRecovery.Snapshot current) {
        return !routeSelecting && definitiveConnectionFailure(connectionPlan, serverRuntimes.get(serverProfiles.selected),
                new ConnectionPlan.Endpoint(connectionMode, origin), current);
    }

    private boolean definitiveConnectionFailure(ConnectionPlan plan, ServerRuntime runtime, ConnectionPlan.Endpoint currentEndpoint,
                                                ConnectionRecovery.Snapshot current) {
        if (!current.problem.equals("endpoint") || runtime != null && runtime.selecting) return false;
        for (ConnectionPlan.Endpoint endpoint : plan.candidates(null)) {
            ConnectionRecovery.Snapshot sample = endpoint.equals(currentEndpoint) ? current
                    : runtime == null ? null : runtime.health.get(endpoint);
            if (sample == null || !sample.problem.equals("endpoint")) return false;
        }
        return true;
    }

    private void expire() {
        forgetAccount();
        stopStreams();
        clearWorkspace();
        loginErrorText = tr("Your session expired. Sign in again.", "会话已过期，请重新登录。");
        render();
    }

    private void logout() {
        api(
                "/logout",
                object(),
                value -> {
                    forgetAccount();
                    serverWorkspaces.remove(serverProfiles.selected);
                    ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
                    if (runtime != null) runtime.candidate = null;
                    stopStreams();
                    clearWorkspace();
                    render();
                });
    }

    private LinearLayout card(String title) {
        LinearLayout view = padded(16, 16);
        view.setBackground(ui.background(ui.sidebar, 12, true));
        view.addView(ui.bold(title, 16));
        gap(view, 12);
        return view;
    }

    private void settingsPage() {
        boolean dark = theme.equals("dark") || theme.equals("system")
                && (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
        NativeUi settingsUi = new NativeUi(this, dark, locale());
        if (settingsDialog != null && connectionSettings != null && settingsDialog.matchesAppearance(settingsUi)
                && serverProfiles.selected.equals(settingsDialog.content.getTag())) {
            settingsDialog.content.title.setText(serverProfiles.active().name + " · " + settingsUi.t("Settings"));
            serverSettings.bind(serverProfiles.active());
            updateConnectionSettings();
            return;
        }
        boolean show = settingsDialog == null;
        if (show) settingsDialog = new NativeSettings(settingsUi, theme, language, appVersion(),
                this::updatePreferences, this::closeSettings, false);
        else settingsDialog.bind(settingsUi, theme, language, body -> settingsAdditions(settingsUi, body));
        if (show) {
            settingsAdditions(settingsUi, settingsDialog.content.additions);
            settingsDialog.show();
        }
        settingsDialog.content.title.setText(serverProfiles.active().name + " · " + settingsUi.t("Settings"));
        settingsDialog.content.setTag(serverProfiles.selected);
        updateConnectionSettings();
    }

    private void settingsAdditions(NativeUi settingsUi, LinearLayout body) {
        serverSettings = new NativeServerSettings(settingsUi, serverProfiles.active(),
                enabled -> enableServer(serverProfiles.selected, enabled), () -> editServer(false));
        body.addView(serverSettings);
        gap(body, 16);
        connectionSettings = new NativeConnectionSettings(settingsUi, connectionDraft,
                this::editConnectionPlan, this::applyConnectionPlan, this::disconnect, this::enrollTailscale);
        serverInput = connectionSettings.input(connectionDraft.selected);
        connectionText = connectionSettings.diagnostics;
        connectionHint = connectionSettings.note;
        body.addView(connectionSettings);
        gap(body, 24);
        Button delete = settingsUi.button(settingsUi.t("Delete server"), false, this::deleteServer);
        delete.setTextColor(settingsUi.error);
        body.addView(delete, new LinearLayout.LayoutParams(-1, -2));
    }

    private void deleteServer() {
        ServerProfiles.Server removed = serverProfiles.active();
        if (removed == null) return;
        closeSettings();
        removed.enabled = false;
        ServerRuntime previous = serverRuntimes.remove(removed.id);
        stopServerFeed(previous);
        ConnectionRouter<Client> previousRouter = previous == null ? connectionRouter : previous.router;
        if (previousRouter != null) previousRouter.stop();
        Client previousClient = client;
        stopStreams(); clearWorkspace(); serverWorkspaces.remove(removed.id);
        ++generation; monitor.stop(); recovery.stop(); main.removeCallbacks(connectionRetry);
        connectionWanted = false; routeSelecting = routeLoginRequired = connected = connecting = false;
        backend = "codex"; model = effort = permission = "";
        client = null; connectionRouter = null; origin = authURL = connectionMessage = networkMessage = "";
        connectionInfo = null; httpsState = "Not checked"; tailnetState = "Disconnected";
        routeStates.clear(); nextRouteProbe = 0; routeFailures = 0;
        serverProfiles.remove(removed.id);
        ServerProfiles.Server next = serverProfiles.active();
        connectionPlan = connectionDraft = next == null ? new ConnectionPlan("", "", ConnectionMode.DIRECT) : next.plan;
        ServerRuntime runtime = next == null ? null : serverRuntimes.get(next.id);
        if (runtime != null) { connectionRouter = runtime.router; routeStates.putAll(runtime.states); }
        restoreServerWorkspace();
        saveServers();
        // The serial connection worker closes the removed node before erasing only its state.
        connectionWorker.execute(() -> {
            if (previousRouter == null && previousClient != null) try { previousClient.close(); } catch (Exception ignored) {}
            List<String> directories = new ArrayList<>();
            for (ConnectionMode mode : ConnectionMode.values()) directories.add(removed.directory(getNoBackupFilesDir(), mode));
            directories.add(removed.sessionDirectory(getNoBackupFilesDir()));
            for (String path : directories) try {
                java.nio.file.Path directory = new File(path).toPath();
                if (!java.nio.file.Files.exists(directory)) continue;
                java.nio.file.Files.walkFileTree(directory, new java.nio.file.SimpleFileVisitor<java.nio.file.Path>() {
                    @Override public java.nio.file.FileVisitResult visitFile(java.nio.file.Path file, java.nio.file.attribute.BasicFileAttributes attributes) throws IOException {
                        java.nio.file.Files.delete(file); return java.nio.file.FileVisitResult.CONTINUE;
                    }
                    @Override public java.nio.file.FileVisitResult postVisitDirectory(java.nio.file.Path folder, IOException error) throws IOException {
                        if (error != null) throw error;
                        java.nio.file.Files.delete(folder); return java.nio.file.FileVisitResult.CONTINUE;
                    }
                });
            } catch (IOException ignored) { /* A removed profile can never reuse this node or session. */ }
        });
        render();
        if (next == null || !next.enabled) return;
        if (runtime != null && runtime.candidate != null) adoptConnection(runtime.candidate);
        else if (!connectionPlan.ordered(null).isEmpty()) selectConnections(null, true);
    }

    private void saveServers() {
        getPreferences(MODE_PRIVATE).edit().putString("servers", serverProfiles.encode()).putString("selectedServer", serverProfiles.selected).apply();
    }

    private void rememberAccount() {
        if (user != null && !routeLoginRequired) rememberAccount(serverProfiles.active(), user);
    }

    private void rememberAccount(ServerProfiles.Server server, JSONObject user) {
        if (server == null || user == null || user.optString("id").isEmpty()) return;
        JSONObject account = ServerProfiles.displayAccount(user);
        ServerWorkspace cached = serverWorkspaces.get(server.id);
        if (cached != null && !cached.userID.equals(account.optString("id"))) serverWorkspaces.remove(server.id);
        if (server.account.toString().equals(account.toString())) return;
        server.account = account; saveServers();
    }

    private void forgetAccount() {
        workspaceUserID = ""; workspaceCached = false;
        forgetAccount(serverProfiles.active());
    }

    private void forgetAccount(ServerProfiles.Server server) {
        if (server == null) return;
        serverWorkspaces.remove(server.id);
        ServerRuntime runtime = serverRuntimes.get(server.id);
        stopServerFeed(runtime);
        if (runtime != null && runtime.candidate != null) {
            ConnectionRouter.Candidate<Client> previous = runtime.candidate;
            runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, previous.client, previous.health, null, true);
        }
        if (server.account.length() == 0) return;
        server.account = new JSONObject(); saveServers();
    }

    private void saveServerWorkspace() {
        String owner = user != null && !routeLoginRequired ? user.optString("id") : workspaceUserID;
        if (owner.isEmpty() || serverProfiles.active() == null) return;
        if ((user == null || routeLoginRequired) && !owner.equals(serverProfiles.active().account.optString("id"))) return;
        captureDraft();
        ServerWorkspace state = serverWorkspaces.get(serverProfiles.selected);
        if (state == null || !owner.equals(state.userID)) state = new ServerWorkspace();
        state.userID = owner; state.projectID = projectID; state.chatID = chatID; state.chat = chat;
        state.backend = backend; state.model = model; state.effort = effort; state.permission = permission;
        state.projects = projects; state.chats = chats; state.agents = agents; state.profiles = profiles;
        state.agentsInitialized = agentsInitialized;
        state.cached = workspaceCached || projects.length() != 0 || chats.length() != 0;
        state.sidebar.copyFrom(sidebarState); state.navigation.copyFrom(navigation);
        state.directory.copyFrom(profileCache); state.history.copyFrom(historyScrollState);
        state.activityStates.clear(); state.activityStates.putAll(activityStates);
        state.drafts.clear();
        for (Map.Entry<String, Draft> entry : drafts.entrySet()) {
            Draft copy = new Draft(); copy.text = entry.getValue().text; copy.files.addAll(entry.getValue().files);
            state.drafts.put(entry.getKey(), copy);
        }
        serverWorkspaces.put(serverProfiles.selected, state);
        workspaceUserID = owner; workspaceCached = state.cached;
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        if (user != null && !routeLoginRequired && runtime != null && runtime.candidate != null && runtime.candidate.client == client) {
            ConnectionRouter.Candidate<Client> previous = runtime.candidate;
            runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, previous.client, previous.health,
                    new ConnectionRouter.Session(owner, object("user", user, "csrf", csrf).toString()));
        }
    }

    private void restoreServerWorkspace() {
        ServerProfiles.Server server = serverProfiles.active();
        if (server == null) return;
        String owner = user == null ? server.account.optString("id") : user.optString("id");
        if (!workspaceUserID.isEmpty()) {
            if (workspaceUserID.equals(owner)) return;
            clearWorkspaceData(true);
        }
        ServerWorkspace state = serverWorkspaces.get(serverProfiles.selected);
        if (state == null) return;
        if (!state.userID.equals(owner)) { serverWorkspaces.remove(serverProfiles.selected); return; }
        projectID = state.projectID; chatID = state.chatID; chat = state.chat;
        backend = state.backend; model = state.model; effort = state.effort; permission = state.permission;
        projects = state.projects; chats = state.chats; agents = state.agents; profiles = state.profiles;
        agentsInitialized = state.agentsInitialized;
        sidebarState.copyFrom(state.sidebar); navigation.copyFrom(state.navigation);
        profileCache.copyFrom(state.directory); historyScrollState.copyFrom(state.history);
        activityStates.putAll(state.activityStates); drafts.putAll(state.drafts);
        workspaceUserID = state.userID; workspaceCached = state.cached;
    }

    private boolean hasWorkspaceCache() {
        return workspaceCached && serverProfiles.active() != null
                && workspaceUserID.equals(serverProfiles.active().account.optString("id"));
    }

    private void selectServer(String id) {
        if (serverProfiles.selected.equals(id)) return;
        ServerProfiles.Server next = null;
        for (ServerProfiles.Server server : serverProfiles.entries) if (server.id.equals(id)) next = server;
        if (next == null) return;
        ServerProfiles.Server previous = serverProfiles.active();
        rememberAccount();
        if (serverProfiles.active() != null) serverProfiles.active().plan = connectionDraft;
        saveServerWorkspace(); closeDesktop();
        parkWorkspaceStream(previous);
        stopStreams();
        if (sidebar != null && panel.equals("sidebar")) deferWorkspacePage();
        clearWorkspace(false);
        backend = "codex"; model = effort = permission = "";
        ++generation; monitor.stop(); recovery.stop(); main.removeCallbacks(connectionRetry);
        routeSelecting = false; routeLoginRequired = false; connected = connecting = false;
        client = null; connectionRouter = null; origin = authURL = connectionMessage = networkMessage = "";
        connectionInfo = null; httpsState = "Not checked"; tailnetState = "Disconnected";
        serverProfiles.selected = next.id;
        connectionPlan = connectionDraft = next.plan;
        routeStates.clear(); nextRouteProbe = 0; routeFailures = 0;
        ServerRuntime runtime = serverRuntimes.get(next.id);
        if (runtime != null) { connectionRouter = runtime.router; routeStates.putAll(runtime.states); }
        restoreServerWorkspace();
        saveServers(); panel = "sidebar";
        if (next.enabled && runtime != null && runtime.candidate != null) adoptConnection(runtime.candidate);
        else {
            render();
            if (next.enabled && !connectionPlan.ordered(null).isEmpty()) selectConnections(null, true);
        }
        if (previous != null) syncServerFeed(previous);
    }

    private void deferWorkspacePage() {
        sidebarPageDeferred = true;
        composer = null; composerView = null; composerDock = null;
        modelMenu = null; modelPopover = null; workspaceHeader = null;
        heading = subheading = null;
        if (transcriptViewport != null) transcriptViewport.dispose();
        transcriptViewport = null;
        if (historyScroll != null) historyScroll.dispose();
        historyScroll = null; history = null; historyContent = null; transcriptViews = null;
        connectingView = null; username = password = null; loginError = null; loginSubmit = null;
        ui.beforeDisclosure = view -> {};
        for (int i = root.getChildCount() - 1; i >= 0; i--) if (root.getChildAt(i) != drawer) root.removeViewAt(i);
    }

    private void enableServer(String id, boolean enabled) {
        for (ServerProfiles.Server server : serverProfiles.entries) if (server.id.equals(id)) {
            server.enabled = enabled; saveServers();
            if (id.equals(serverProfiles.selected)) {
                if (enabled) selectConnections(null, true); else disconnect();
            } else {
                ServerRuntime runtime = serverRuntimes.get(id);
                if (!enabled && runtime != null) { stopServerFeed(runtime); runtime.router.stop(); runtime.candidate = null; runtime.selecting = false; }
                if (enabled) probeServer(server);
            }
            renderNavigation(); return;
        }
    }

    private void refreshServerConnections() {
        refreshServerConnections(false);
    }

    private void refreshServerConnections(boolean force) {
        if (!foreground || destroyed) return;
        for (ServerProfiles.Server server : serverProfiles.entries)
            if (server.enabled && !server.id.equals(serverProfiles.selected)) probeServer(server, force);
        main.removeCallbacks(serverRefresh); main.postDelayed(serverRefresh, 15000);
    }

    private void stopServerFeed(ServerRuntime runtime) {
        if (runtime == null || runtime.feed == null) return;
        ServerFeed feed = runtime.feed; runtime.feed = null;
        if (feed.data != null) feed.data.stop();
        if (feed.events != null) feed.events.stop();
    }

    private void parkWorkspaceStream(ServerProfiles.Server server) {
        if (server == null || !server.enabled || user == null || routeLoginRequired || client == null || workspaceEvents == null) return;
        ServerRuntime runtime = serverRuntimes.get(server.id);
        ServerWorkspace cache = serverWorkspaces.get(server.id);
        if (runtime == null || cache == null || runtime.candidate == null || runtime.candidate.client != client) return;
        stopServerFeed(runtime);
        ServerFeed feed = new ServerFeed(client, user.optString("id"), csrf, locale(), cache);
        feed.events = workspaceEvents;
        feed.stream = workspaceStream; feed.desktopAvailable = desktopAvailable; feed.presence = presence;
        runtime.feed = feed;
        workspaceEvents = null; workspaceStream = null;
    }

    private boolean serverFeedCurrent(ServerProfiles.Server server, ServerRuntime runtime, ServerFeed feed) {
        return !destroyed && foreground && server.enabled && !server.id.equals(serverProfiles.selected)
                && serverRuntimes.get(server.id) == runtime && runtime.feed == feed
                && runtime.candidate != null && runtime.candidate.client == feed.client
                && serverWorkspaces.get(server.id) == feed.cache;
    }

    /** Inactive tabs keep their own metadata stream without changing the displayed workspace. */
    private void syncServerFeed(ServerProfiles.Server server) {
        ServerRuntime runtime = serverRuntimes.get(server.id);
        ConnectionRouter.Candidate<Client> candidate = runtime == null ? null : runtime.candidate;
        if (!foreground || destroyed || !server.enabled || server.id.equals(serverProfiles.selected) || candidate == null) {
            stopServerFeed(runtime); return;
        }
        if (candidate.session == null || !candidate.health.healthy()) {
            if (definitiveConnectionFailure(server.plan, runtime, candidate.endpoint, candidate.health)) {
                stopServerFeed(runtime);
                ServerWorkspace cache = serverWorkspaces.get(server.id);
                if (cache != null) { cache.projects = new JSONArray(); cache.chats = new JSONArray(); cache.cached = false; cache.navigation.reset(); }
                return;
            }
            // A transient health/auth read can fail while the same WebSocket is still live.
            if (runtime.feed != null && runtime.feed.client == candidate.client && !candidate.sessionChecked) return;
            stopServerFeed(runtime); return;
        }
        final String auth;
        try { auth = new JSONObject(candidate.session.json).getString("csrf"); }
        catch (Exception error) { stopServerFeed(runtime); return; }
        if (auth.isEmpty()) { stopServerFeed(runtime); return; }
        ServerWorkspace cache = serverWorkspaces.get(server.id);
        if (cache == null || !cache.userID.equals(candidate.session.userID)) {
            cache = new ServerWorkspace(); cache.userID = candidate.session.userID;
            serverWorkspaces.put(server.id, cache);
        }
        ServerFeed current = runtime.feed;
        boolean reusable = current != null && current.client == candidate.client && current.userID.equals(candidate.session.userID)
                && current.csrf.equals(auth) && current.locale.equals(locale()) && current.cache == cache;
        if (reusable && current.data != null) return;
        if (!reusable) stopServerFeed(runtime);
        ServerFeed feed = reusable ? current : new ServerFeed(candidate.client, candidate.session.userID, auth, locale(), cache);
        runtime.feed = feed;
        String key = UUID.randomUUID().toString();
        feed.data = new NativeWorkspaceData(workspaceWorker, new NativeWorkspaceData.Dispatcher() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
        }, cache.directory);
        feed.data.bind(key, path -> request(feed.client, path, null, feed.csrf, feed.locale), new NativeWorkspaceData.Listener() {
            public void navigation(JSONObject value) {
                if (serverFeedCurrent(server, runtime, feed)) acceptServerNavigation(feed.cache, value);
            }
            public void agents(JSONArray value) {
                if (serverFeedCurrent(server, runtime, feed)) feed.cache.agents = value;
            }
            public void profiles(JSONObject value) {
                if (serverFeedCurrent(server, runtime, feed)) acceptServerProfiles(server, feed.cache, value);
            }
            public void failed(Exception error, boolean initial) {
                if (serverFeedCurrent(server, runtime, feed) && error instanceof ApiFailure && ((ApiFailure) error).status == 401)
                    forgetAccount(server);
            }
        }, feed.events == null || !feed.events.ready());
        NativeWorkspaceEvents.Listener<EventStream> listener = new NativeWorkspaceEvents.Listener<EventStream>() {
            public void connection(EventStream stream) { feed.stream = stream; }
            public void connected(boolean value) {
                if (!serverFeedCurrent(server, runtime, feed)) return;
                feed.data.ready(value);
                if (!value) feed.desktopAvailable = false;
            }
            public void expired() { if (serverFeedCurrent(server, runtime, feed)) forgetAccount(server); }
            public void authFailed(Exception error) {
                if (serverFeedCurrent(server, runtime, feed) && error instanceof ApiFailure && ((ApiFailure) error).status == 401)
                    forgetAccount(server);
            }
            public void failed() { if (serverFeedCurrent(server, runtime, feed)) probeServer(server, true); }
            public void event(String kind, String raw) {
                if (!java.util.Arrays.asList("navigation", "agents", "profiles", "desktop", "presence").contains(kind)) return;
                EventStream stream = feed.stream;
                workspaceDecode.execute(() -> {
                    if (destroyed) return;
                    try {
                        Object value = new JSONTokener(raw).nextValue();
                        main.post(() -> {
                            routeServerEvent(server, feed.client, feed.events, stream, feed.userID, feed.csrf, kind, value);
                        });
                    } catch (Exception ignored) { /* Keep this tab's last valid snapshot until the next update. */ }
                });
            }
        };
        if (feed.events != null && feed.events.handoff(key, listener)) {
            feed.events.visibility(false);
            return;
        }
        feed.events = new NativeWorkspaceEvents<>(workspaceSocketWorker, workspaceProbeWorker, new NativeWorkspaceEvents.Scheduler() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
            public long now() { return android.os.SystemClock.elapsedRealtime(); }
            public double random() { return Math.random(); }
        });
        feed.events.bind(key, new NativeWorkspaceEvents.Source<EventStream>() {
            public EventStream open(NativeWorkspaceEvents.Events events) throws Exception {
                return feed.client.subscribeWorkspace(feed.csrf, feed.locale, events::event);
            }
            public void close(EventStream stream) { closeStream(stream); }
            public void send(EventStream stream, String type, boolean visible) throws Exception {
                stream.send(object("type", type, "visible", visible).toString());
            }
            public void probe(EventStream stream) throws Exception { stream.probe(); }
            public void checkAuth() throws Exception { request(feed.client, "/me", null, feed.csrf, feed.locale); }
        }, listener);
        feed.events.visibility(false);
    }

    /** Decoding may cross a tab switch; route the frame by its still-live server/socket owner. */
    private void routeServerEvent(ServerProfiles.Server server, Client sourceClient, NativeWorkspaceEvents<EventStream> source,
                                  EventStream stream, String account, String auth, String kind, Object value) {
        if (destroyed || server == null || source == null) return;
        if (server.id.equals(serverProfiles.selected) && client == sourceClient && workspaceEvents == source && workspaceStream == stream
                && user != null && user.optString("id").equals(account) && csrf.equals(auth) && !routeLoginRequired) {
            try { applyWorkspaceEvent(kind, value, workspaceEpoch); } catch (Exception error) { toast(error); }
            return;
        }
        ServerRuntime runtime = serverRuntimes.get(server.id);
        ServerFeed feed = runtime == null ? null : runtime.feed;
        if (feed == null || !serverFeedCurrent(server, runtime, feed) || feed.client != sourceClient || feed.events != source
                || feed.stream != stream || !feed.userID.equals(account) || !feed.csrf.equals(auth)) return;
        if (kind.equals("navigation") && value instanceof JSONObject) feed.data.navigation((JSONObject) value);
        else if (kind.equals("agents") && value instanceof JSONArray) feed.cache.agents = (JSONArray) value;
        else if (kind.equals("profiles") && value instanceof JSONObject) acceptServerProfiles(server, feed.cache, (JSONObject) value);
        else if (kind.equals("desktop") && value instanceof JSONObject) feed.desktopAvailable = ((JSONObject) value).optBoolean("available");
        else if (kind.equals("presence") && value instanceof JSONArray) feed.presence = (JSONArray) value;
    }

    private void acceptServerNavigation(ServerWorkspace cache, JSONObject value) {
        cache.projects = array(value, "projects");
        JSONArray incoming = array(value, "chats");
        cache.chats = NativeNavigation.merge(cache.chats, incoming);
        NativeNavigation.Selection next = cache.navigation.select(cache.projectID, cache.chatID, cache.projects, incoming);
        if (!next.project.equals(cache.projectID) || !next.chat.equals(cache.chatID)) { cache.chat = null; cache.permission = ""; }
        cache.projectID = next.project; cache.chatID = next.chat;
        if (cache.chat != null) for (int i = 0; i < incoming.length(); i++) {
            JSONObject metadata = at(incoming, i);
            if (!cache.chatID.equals(metadata.optString("id"))) continue;
            cache.chat = NativeChatState.copy(cache.chat);
            for (String field : new String[] {"title", "participants", "can_control"}) NativeChatState.put(cache.chat, field, metadata.opt(field));
            break;
        }
        cache.sidebar.select(cache.projectID, cache.chatID, cache.projects, cache.chats);
        cache.cached = true;
    }

    private void acceptServerProfiles(ServerProfiles.Server server, ServerWorkspace cache, JSONObject value) {
        if (!cache.directory.accept(value)) return;
        cache.profiles = cache.directory.people;
        for (int i = 0; i < cache.profiles.length(); i++) {
            JSONObject person = at(cache.profiles, i);
            if (cache.userID.equals(person.optString("id"))) { rememberAccount(server, person); break; }
        }
    }

    private void probeServer(ServerProfiles.Server server) {
        probeServer(server, false);
    }

    private void probeServer(ServerProfiles.Server server, boolean force) {
        if (!foreground || !server.enabled || server.plan.ordered(null).isEmpty()) return;
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(server.id, key -> new ServerRuntime());
        if (runtime.selecting) return;
        ConnectionRouter.Candidate<Client> current = runtime.candidate;
        if (!force && current != null && current.health.healthy() && current.sessionChecked) {
            long age = current.client.responseAgeMillis();
            if (age >= 0 && age < ConnectionRecovery.QUIET_CHECK_MILLIS) return;
        }
        runtime.selecting = true;
        String account = runtime.candidate == null || runtime.candidate.session == null ? "" : runtime.candidate.session.userID;
        boolean awaitingLogin = current != null && (current.health.problem.equals("login") || current.health.problem.equals("approval")
                || current.health.problem.equals("starting") || current.health.healthy() && current.session == null);
        List<ConnectionPlan.Endpoint> endpoints = server.plan.autoSwitch && !awaitingLogin
                ? server.plan.ordered(current == null ? null : current.endpoint.mode)
                : server.plan.candidates(current == null ? server.plan.selected : current.endpoint.mode);
        routes(server).select(endpoints, account, false, true, new ConnectionRouter.Listener<Client>() {
            public void replacing(Client previous) { stopServerFeed(runtime); runtime.candidate = null; }
            public void sampled(ConnectionPlan.Endpoint endpoint, ConnectionRecovery.Snapshot health, boolean signedIn) {
                runtime.health.keySet().retainAll(server.plan.ordered(null));
                runtime.health.put(endpoint, health);
                runtime.states.put(endpoint.mode, health.healthy() ? tr("Available", "可用") : tr("Unavailable", "不可用"));
            }
            public void selected(ConnectionRouter.Candidate<Client> candidate) {
                runtime.selecting = false;
                if (!server.enabled || destroyed) return;
                runtime.candidate = candidate;
                if (candidate.session != null) try {
                    rememberAccount(server, new JSONObject(candidate.session.json).optJSONObject("user"));
                } catch (Exception ignored) {}
                else if (candidate.sessionChecked && !server.id.equals(serverProfiles.selected)) forgetAccount(server);
                if (server.id.equals(serverProfiles.selected)) { routeStates.putAll(runtime.states); adoptConnection(candidate); }
                else syncServerFeed(server);
                renderNavigation();
            }
            public void unavailable() { runtime.selecting = false; renderNavigation(); }
        });
    }

    private void editServer(boolean adding) {
        if (serverDialog != null && serverDialog.isShowing()) return;
        serverDialog = new NativeServerDialog(ui, adding ? "" : serverProfiles.active().name, adding, name -> {
            if (adding) { ServerProfiles.Server server = serverProfiles.add(name); selectServer(server.id); openSettings(); }
            else { serverProfiles.active().name = name; saveServers(); settingsPage(); }
        });
        serverDialog.show();
    }

    private void editConnectionPlan(ConnectionPlan plan) {
        connectionDraft = plan;
        if (plan.autoSwitch != connectionPlan.autoSwitch) {
            connectionPlan = connectionPlan.automatic(plan.autoSwitch);
            getPreferences(MODE_PRIVATE).edit().putBoolean("automaticConnections", plan.autoSwitch).apply();
            serverProfiles.active().plan = connectionPlan; saveServers();
            if (connectionRouter != null) connectionRouter.cancel();
            routeSelecting = false;
        }
        updateConnectionSettings();
    }

    private void saveLocalPreferences() {
        getPreferences(MODE_PRIVATE)
                .edit()
                .putString("theme", theme)
                .putString("language", language)
                .apply();
    }

    private void setDesktopAvailable(boolean available) {
        desktopAvailable = available;
        if (sidebar != null) sidebar.desktop(available, this::openDesktop);
    }

    private void openDesktop() {
        if (desktopDialog != null || !foreground || destroyed || user == null || client == null) return;
        final Client captured = client;
        final String auth = csrf, loc = locale();
        captureDraft(); closeSettings(); closeDrawer(); closePeople(); closeImagePreview();
        desktopDialog = new NativeDesktopDialog(ui, new NativeDesktopConnection.Transport() {
            public JSONObject request(String path, boolean post) throws Exception {
                return (JSONObject) MainActivity.this.request(captured, path, post ? object() : null, auth, loc);
            }
            public NativeDesktopConnection.Wire open(String token, NativeDesktopConnection.Listener listener) throws Exception {
                spoolnet.DesktopStream stream = captured.openDesktop(token, new spoolnet.DesktopListener() {
                    public void onOpen() { listener.opened(); }
                    public void onMessage(byte[] data, boolean binary) { listener.message(data, binary); }
                    public void onClosed(long status) { listener.ended(); }
                });
                return new NativeDesktopConnection.Wire() {
                    public void send(String command) throws Exception { stream.send(command); }
                    public void close() { stream.close(); }
                };
            }
        }, io, () -> { if (desktopDialog != null && !desktopDialog.isShowing()) desktopDialog = null; }, serverProfiles.active().name);
        desktopDialog.show();
    }

    private void closeDesktop() {
        NativeDesktopDialog current = desktopDialog;
        desktopDialog = null;
        if (current != null) current.dismiss();
    }

    private void updatePreferences(String nextTheme, String nextLanguage) {
        if (theme.equals(nextTheme) && language.equals(nextLanguage)) return;
        theme = nextTheme; language = nextLanguage;
        saveLocalPreferences();
        render();
        for (ServerProfiles.Server server : serverProfiles.entries) if (!server.id.equals(serverProfiles.selected)) syncServerFeed(server);
    }

    private void updateConnectionSettings() {
        if (!settingsOpen || connectionText == null) return;
        String message = networkMessage == null || networkMessage.isEmpty() ? "" : "\n\n" + networkMessage;
        if (connectionMode == ConnectionMode.DIRECT) {
            connectionHint.setText(localizedString(R.string.connection_direct_note));
            String summary = localizedString(R.string.connection_direct_summary, httpsState, message);
            if (connectionSettings != null) bindConnectionSettings(summary);
            return;
        }
        connectionHint.setText("");
        String state =
                tailnetState.equals("Running")
                        ? tr("Running", "运行中")
                        : tailnetState.equals("NeedsLogin")
                                ? tr("Sign-in required", "等待登录")
                                : tailnetState.equals("NeedsMachineAuth")
                                        ? tr("Device approval required", "等待设备批准")
                                        : tailnetState.equals("Starting")
                                                ? tr("Starting", "启动中")
                                                : tr("Disconnected", "未连接");
        String path = tr("Waiting for node probe", "等待节点探测"),
                latency = "—",
                target = "—",
                sampled = "—";
        if (connectionInfo != null) {
            String kind = connectionInfo.optString("path");
            path =
                    kind.equals("direct")
                            ? tr("Direct", "直连")
                            : kind.equals("derp")
                                    ? "DERP " + connectionInfo.optString("relay")
                                    : kind.equals("peer-relay")
                                            ? "Peer Relay"
                                            : tr("Unknown", "未知");
            if (connectionInfo.has("latencyMs") && !connectionInfo.isNull("latencyMs"))
                latency =
                        String.format(
                                Locale.getDefault(),
                                "%.1f ms",
                                connectionInfo.optDouble("latencyMs"));
            target = connectionInfo.optString("targetIP", "—");
            long time = connectionInfo.optLong("sampledAtMs");
            if (time > 0) sampled = DateFormat.getTimeInstance().format(new java.util.Date(time));
        }
        String summary =
                localizedString(
                        R.string.connection_summary,
                        state,
                        path,
                        latency,
                        target,
                        httpsState,
                        sampled,
                        message);
        if (connectionSettings != null) bindConnectionSettings(summary);
    }

    private void bindConnectionSettings(String summary) {
        connectionSettings.bind(routeSelecting ? connectionPlan.selected : connectionMode, connected, client != null || routeSelecting, routeSelecting,
                !connectionDraft.tailscale.isEmpty(), routeStates, summary,
                serverConnectionStates().getOrDefault(serverProfiles.selected, "disconnected"));
    }

    private void workspacePage() {
        LinearLayout page = ui.workspace();
        NativeUi.Header header = ui.new Header(tr("Workspace", "工作空间"), projectName(),
                this::openSidebar, this::openOnlinePeople, () -> openFiles("."));
        workspaceHeader = header;
        updateHeaderPeople();
        heading = header.title;
        subheading = header.subtitle;
        page.addView(header);
        page.addView(ui.divider());
        // Delta-based fling steps preserve corrections when virtual rows change height.
        history = new NestedScrollView(this);
        history.setFillViewport(true);
        history.setVerticalScrollBarEnabled(false);
        historyContent = padded(12, 25);
        transcriptViews = new NativeKeyedViews(historyContent);
        history.addView(historyContent);
        historyScrollState.select(authEpoch + "\n" + selectionEpoch + "\n" + chatID);
        historyScroll = new NativeHistoryScroll(history, historyContent, historyScrollState, 90 * getResources().getDisplayMetrics().density);
        ui.beforeDisclosure = historyScroll::preservePosition;
        // Keep message/shadow overflow inside the scrolling viewport. The page
        // itself permits overflow so the composer's shadow can reach the history.
        FrameLayout historyViewport = new FrameLayout(this);
        historyViewport.addView(history, new FrameLayout.LayoutParams(-1, -1));
        page.addView(historyViewport, new LinearLayout.LayoutParams(-1, 0, 1));
        composerView = ui.new Composer(composerHint(), this::pickFiles, this::chooseModel,
                this::choosePermission, this::sendOrStop);
        composer = composerView.input;
        fileBar = composerView.attachments;
        modelButton = composerView.model;
        permissionButton = composerView.permission;
        send = composerView.send;
        composer.setText(draft().text);
        composer.addTextChangedListener(new TextWatcher() {
            public void beforeTextChanged(CharSequence value, int start, int count, int after) {}
            public void onTextChanged(CharSequence value, int start, int before, int count) {
                draft().text = value.toString();
                updateComposer();
            }
            public void afterTextChanged(Editable value) {}
        });
        page.addView(composerView, new LinearLayout.LayoutParams(-1, -2));
        final NativeHistoryScroll behavior = historyScroll;
        composerDock = new NativeComposerDock(ui, page, composerView, () -> {
            if (foreground && !destroyed && historyScroll == behavior && user != null) behavior.latest();
        });
        behavior.onFollowing(composerDock::following);
        root.addView(composerDock, new FrameLayout.LayoutParams(-1, -1));
        renderFiles();
        renderChat();
        updateComposer();
    }

    private String composerHint() {
        if (projectID.isEmpty()) return tr("Waiting for project access", "等待项目访问权限");
        if (!writable()) return tr("Read-only", "只读");
        return ui.t("Message {agent}", "agent", backend.equals("claude") ? "Claude" : "Codex");
    }

    private String projectName() {
        for (int i = 0; i < projects.length(); i++)
            if (at(projects, i).optString("id").equals(projectID))
                return at(projects, i).optString("name");
        return tr("Local agent workspace", "本地 agent 工作空间");
    }

    private boolean writable() {
        boolean authorized = false;
        for (int i = 0; i < projects.length(); i++) authorized |= at(projects, i).optString("id").equals(projectID);
        if (user == null || !authorized || !chatID.isEmpty() && (chat == null || !chatID.equals(chat.optString("id")))) return false;
        if (chat != null) return chat.optBoolean("can_control", false);
        for (int i = 0; i < projects.length(); i++)
            if (at(projects, i).optString("id").equals(projectID))
                return at(projects, i).optBoolean("can_create", true);
        return false;
    }

    private void updateComposer() {
        renderNavigation();
        if (composer == null || composerView == null) return;
        JSONObject activeTurn = NativeChatState.runningTurn(chat);
        boolean running = chat != null && chat.optString("status").equals("running");
        boolean stopping = activeTurn != null && (stoppingTurns.contains(activeTurn.optString("id"))
                || activeSubmission(activeTurn) != null && pendingSend.stopRequested);
        composerView.commands.bind(writable(), busy, running);
        composerView.commands.notice(goalNotice, () -> { goalNotice = ""; updateComposer(); });
        composerView.commands.error(goalError);
        composer.setEnabled(writable());
        String hint = composerHint();
        if (!TextUtils.equals(composer.getHint(), hint)) composer.setHint(hint);
        composerView.add.setEnabled(!busy && writable());
        modelButton.setEnabled(!busy && !running && writable());
        permissionButton.setEnabled(!busy && !running && writable());
        modelButton.setVisibility(projectID.isEmpty() ? View.GONE : View.VISIBLE);
        permissionButton.setVisibility(projectID.isEmpty() ? View.GONE : View.VISIBLE);
        send.setKind(activeTurn == null ? "send" : stopping ? "spinner" : "stop");
        send.setContentDescription(activeTurn == null ? ui.t("Send message") : ui.t(stopping ? "Stopping…" : "Stop response"));
        send.setEnabled(writable() && (activeTurn != null ? !stopping : !busy && selectedAgentAvailable() && permissionReady()
                && (!NativeGoalCommand.trim(composer.getText().toString()).isEmpty() || !draft().files.isEmpty())));
        String modelName = model.isEmpty() ? tr("CLI default", "CLI 默认") : model;
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = at(agents, i);
            if (!agent.optString("backend").equals(backend)) continue;
            JSONArray models = array(agent.optJSONObject("catalog"), "models");
            for (int j = 0; j < models.length(); j++)
                if (at(models, j).optString("model").equals(model))
                    modelName = at(models, j).optString("displayName", model);
        }
        modelButton.bind(modelName, backend.equals("claude") ? "claude" : "codex",
                tr("Choose agent and model", "选择 Agent 与模型") + ": " + modelName);
        String effectivePermission = permission.isEmpty() ? backend.equals("claude") ? "acceptEdits" : "ask" : permission;
        permissionButton.bind(permissionLabel(effectivePermission),
                effectivePermission.equals("ask") || effectivePermission.equals("manual") ? "shield-question" : "shield-check",
                tr("Permissions", "权限") + ": " + permissionLabel(effectivePermission));
        permissionButton.setDanger(permission.equals("bypassPermissions"));
    }

    private void openSidebar() {
        openSidebar(true);
    }

    private void openSidebar(boolean animate) {
        closeDrawer(false);
        panel = "sidebar";
        NativeDrawer overlay = new NativeDrawer(ui, false);
        drawer = overlay;
        sidebar = new NativeSidebar(ui, sidebarState, this::person, this::closeDrawer, this::openChat, this::newChat,
                () -> { closeDrawer(); openAvatarPicker(); }, this::openSettings, this::logout, this::loginFromSidebar, this::openAppearance);
        overlay.sheet.addView(sidebar, new LinearLayout.LayoutParams(-1, -1));
        root.addView(overlay, new FrameLayout.LayoutParams(-1, -1));
        renderNavigation();
        overlay.show(animate);
    }

    private void closeDrawer() {
        closeDrawer(true);
    }

    private void closeDrawer(boolean showPage) {
        if (fileBrowser != null) fileBrowser.deactivate();
        fileBrowser = null;
        FrameLayout previous = drawer;
        drawer = null;
        if (sidebar != null) sidebar.deactivate();
        sidebar = null;
        panel = "";
        if (previous instanceof NativeDrawer) ((NativeDrawer) previous).dismiss();
        else if (previous != null) root.removeView(previous);
        if (sidebarPageDeferred && showPage) {
            sidebarPageDeferred = false;
            render();
        }
    }

    private Map<String, String> serverConnectionStates() {
        Map<String, String> states = new HashMap<>();
        for (ServerProfiles.Server server : serverProfiles.entries) {
            ServerRuntime runtime = serverRuntimes.get(server.id);
            String state = "disconnected";
            if (server.enabled && !server.plan.ordered(null).isEmpty()) {
                if (runtime != null && runtime.selecting && (runtime.candidate == null || !runtime.candidate.health.healthy())) state = "connecting";
                else if (server.id.equals(serverProfiles.selected)) {
                    if (connected) state = "connected";
                    else if (routeSelecting || connecting && !httpsState.equals("Failed")) state = "connecting";
                    else state = "error";
                } else if (runtime != null && runtime.candidate != null) {
                    ConnectionRecovery.Snapshot health = runtime.candidate.health;
                    state = health.healthy() ? "connected"
                            : health.problem.equals("login") || health.problem.equals("approval") ? "login" : "error";
                } else state = "connecting";
            }
            states.put(server.id, state);
        }
        return states;
    }

    private void renderNavigation() {
        rememberAccount();
        sidebarState.select(projectID, chatID, projects, chats);
        if (!panel.equals("sidebar") || sidebar == null) return;
        sidebar.servers(serverProfiles, serverConnectionStates(), this::selectServer, () -> editServer(true));
        boolean signedIn = user != null && !routeLoginRequired;
        boolean showWorkspace = signedIn || hasWorkspaceCache();
        boolean networkPending = routeSelecting || connecting && !httpsState.equals("Failed");
        boolean pending = networkPending || sessionCheckBusy;
        String action = networkPending ? "Connecting…" : sessionCheckBusy ? "Restoring sign-in…" : !connected || client == null || restoringSession
                ? connectionWanted && serverProfiles.active() != null && serverProfiles.active().enabled ? "Retry" : "Connect"
                : signedIn ? "" : "Sign in";
        JSONObject account = signedIn ? ServerProfiles.displayAccount(person(user.optString("id")))
                : serverProfiles.active() == null ? new JSONObject() : serverProfiles.active().account;
        sidebar.connection(account, action, pending);
        sidebar.bind(signedIn ? person(user.optString("id")) : new JSONObject(), showWorkspace ? projects : new JSONArray(),
                showWorkspace ? chats : new JSONArray(), showWorkspace ? projectID : "", showWorkspace ? chatID : "",
                showWorkspace && chat != null && array(chat, "turns").length() == 0 ? backend : "", busy, !signedIn && showWorkspace);
        sidebar.desktop(desktopAvailable, this::openDesktop);
    }

    private void loginFromSidebar() {
        if (serverProfiles.active() == null) { editServer(true); return; }
        if (routeSelecting || sessionCheckBusy) return;
        if (connectionPlan.ordered(null).isEmpty()) { openSettings(); return; }
        if (!serverProfiles.active().enabled) enableServer(serverProfiles.selected, true);
        else if (!connected || client == null) selectConnections(null, true);
        else if (restoringSession) restoreSession(client, generation);
        else { closeDrawer(); render(); if (username != null) username.requestFocus(); }
    }

    private void refreshWorkspace() {
        bindWorkspaceData(true);
    }

    private void bindWorkspaceData(boolean reload) {
        if (routeLoginRequired || !foreground || destroyed || user == null || client == null) return;
        if (workspaceData == null) workspaceData = new NativeWorkspaceData(workspaceWorker, new NativeWorkspaceData.Dispatcher() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
        }, profileCache);
        final Client captured = client;
        final int token = generation, session = authEpoch;
        final String auth = csrf;
        String key = token + "\n" + session + "\n" + user.optString("id");
        boolean bound = workspaceData.bind(key, path -> request(captured, path, null, auth, locale()), new NativeWorkspaceData.Listener() {
            public void navigation(JSONObject value) { acceptNavigation(value); }
            public void agents(JSONArray value) { acceptAgents(value); }
            public void profiles(JSONObject value) throws Exception { acceptProfiles(value); refreshProfileViews(); }
            public void failed(Exception error, boolean initial) {
                if (token != generation || session != authEpoch || user == null) return;
                if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
                else if (initial) toast(error);
            }
        });
        workspaceData.ready(workspaceReady);
        if (!bound && reload) workspaceData.reload();
        updateMissingProfiles();
    }

    private void acceptAgents(JSONArray value) {
        agents = value;
        if (!agentsInitialized) { agentsInitialized = true; adoptModel(); }
        else updateComposer();
        if (modelPopover != null && modelPopover.isShowing()) modelMenu.bindAgents(agents);
    }

    private void updateMissingProfiles() {
        if (workspaceData != null && user != null) workspaceData.missing(profileCache.missing(chats, chat, presence));
    }

    private void refreshProfileViews() {
        updateHeaderPeople(); renderPeople();
        if (!settingsOpen && historyContent != null) renderChat();
    }

    private void acceptNavigation(JSONObject value) {
        workspaceCached = true;
        workspaceUserID = user == null ? workspaceUserID : user.optString("id");
        projects = array(value, "projects");
        JSONArray incoming = array(value, "chats");
        chats = NativeNavigation.merge(chats, incoming);
        NativeNavigation.Selection selected = navigation.select(projectID, chatID, projects, incoming);
        if (!selected.project.equals(projectID) || !selected.chat.equals(chatID)) {
            goalNotice = goalError = "";
            discardComposerDrafts();
            projectID = selected.project;
            chatID = "";
            chat = null;
            invalidatePermissions();
            permission = "";
            stopHistory();
            ++selectionEpoch;
            stopChat();
            if (!selected.chat.isEmpty()) selectChat(selected.chat);
            else { render(); loadPermissions(); }
        }
        if (chat != null)
            for (int i = 0; i < incoming.length(); i++)
                if (at(incoming, i).optString("id").equals(chatID)) {
                    try {
                        JSONObject next = at(incoming, i);
                        if (fileBrowser != null && next.optDouble("updated_at") > chat.optDouble("updated_at")) fileBrowser.refreshWorkspace();
                        if (next.optDouble("updated_at") >= chat.optDouble("updated_at")) {
                            chat.put("title", next.opt("title"));
                            chat.put("participants", next.opt("participants"));
                        }
                        chat.put("can_control", next.optBoolean("can_control"));
                        if (heading != null) heading.setText(chat.optString("title"));
                        if (subheading != null) subheading.setText(projectName());
                    } catch (Exception ignored) {
                    }
                }
        renderNavigation();
        if (chat != null && !canCreateInProject() && !backend.equals(chat.optString("backend"))) {
            NativeModelSelection selection = NativeModelSelection.chat(agents, chat);
            backend = selection.backend;
            model = selection.model;
            effort = selection.effort;
            invalidatePermissions();
            permission = "";
            loadPermissions();
        }
        updateComposer();
        updateMissingProfiles();
    }

    private void newChat(String id) {
        if (busy) return;
        goalNotice = goalError = "";
        discardComposerDrafts();
        ++draftVersion;
        ++selectionEpoch;
        stopChat();
        closeDrawer(false); sidebarPageDeferred = false;
        chatID = "";
        projectID = id;
        chat = null;
        stopHistory();
        invalidatePermissions();
        permission = "";
        render();
        adoptModel();
        loadPermissions();
    }

    private void openChat(String id) {
        boolean deferred = sidebarPageDeferred;
        closeDrawer(false); sidebarPageDeferred = false;
        if (deferred && id.equals(chatID)) { render(); startChat(); return; }
        selectChat(id);
    }

    /** Restoring a selection from workspace data must preserve the user's open sidebar. */
    private void selectChat(String id) {
        if (id.equals(chatID)) return;
        goalNotice = goalError = "";
        discardComposerDrafts();
        ++selectionEpoch;
        stopChat();
        chatID = id;
        for (int i = 0; i < chats.length(); i++)
            if (at(chats, i).optString("id").equals(id)) projectID = at(chats, i).optString("project_id");
        stopHistory();
        chat = null;
        invalidatePermissions();
        permission = "";
        render();
        startChat();
    }

    // Turn ids whose rows the next renderChat must rebind; null rebinds every turn.
    private java.util.Set<String> pendingRebinds;

    private void renderChat() { renderChat(null); }

    private void renderChat(NativeHistoryScroll.Anchor anchor) {
        java.util.Set<String> rebinds = pendingRebinds; pendingRebinds = null;
        updateChatRefresh();
        updateMissingProfiles();
        if (historyContent == null) return;
        if (optimisticSend != null) {
            boolean visible = false;
            for (int i = 0; i < array(chat, "turns").length(); i++)
                visible |= optimisticSend.turnID.equals(at(array(chat, "turns"), i).optString("id"));
            if (!visible) optimisticSend = null;
        }
        historyScroll.changing(anchor);
        transcriptViews.begin();
        updateHistoryLoading();
        NativeHistoryLoader.Cursor cursor = historyCursor(chat);
        boolean failed = historyLoader != null && historyLoader.failed();
        if (!cursor.error.isEmpty() || cursor.more)
            transcriptViews.use("history-notice", new JSONArray().put(!cursor.error.isEmpty()).put(cursor.more).put(failed),
                    () -> new NativeHistoryNotice(ui, !cursor.error.isEmpty(), cursor.more, failed, this::loadEarlier));
        if (chat == null || array(chat, "turns").length() == 0) {
            heading.setText(projectID.isEmpty() ? tr("Workspace", "工作空间") : tr("New chat", "新对话"));
            subheading.setText(projectName());
            transcriptViews.use("welcome", projectID + "\n" + projectName(), () -> ui.welcome(
                    ui.t(projectID.isEmpty() ? "Prepare a project first." : "What shall we build today?"),
                    projectID.isEmpty() ? ui.t("Ask your desktop administrator to create a project and grant you access.")
                            : ui.t("Describe your idea. Your agent takes it from here."),
                    projectID.isEmpty() ? ui.t("Waiting for project access") : projectName(),
                    ui.t("Run locally. Pick up anytime.")));
            transcriptViews.end();
            historyScroll.changed();
            if (transcriptViewport != null) transcriptViewport.dataChanged();
            updateComposer();
            return;
        }
        heading.setText(chat.optString("title", tr("New chat", "新对话")));
        subheading.setText(projectName());
        Map<String, Integer> occurrences = new HashMap<>();
        boolean virtual = array(chat, "turns").length() > 32 || array(chat.optJSONObject("transcript"), "parts").length() > 256 || transcriptViewport != null;
        if (virtual && transcriptViewport == null)
            transcriptViewport = new NativeTranscriptViewport(history, historyContent, historyScroll, historyScrollState);
        for (int i = 0; i < array(chat, "turns").length(); i++) {
            JSONObject turn = at(array(chat, "turns"), i);
            String id = turn.optString("id");
            String key = new JSONArray().put(id.isEmpty() ? "index" : "turn").put(id.isEmpty() ? i : id)
                    .put(occurrences.merge(id, 1, Integer::sum)).toString();
            if (virtual) {
                JSONObject page = chat;
                NativeTranscriptViewport.Row row = (NativeTranscriptViewport.Row) transcriptViews.use(key + "/viewport", "viewport",
                        () -> new NativeTranscriptViewport.Row(ui));
                int partCount = array(page.optJSONObject("transcript"), "parts").length();
                row.partCount = Math.max(0, Math.min(partCount, turn.optInt("part_end", partCount)) - Math.max(0, turn.optInt("part_start")));
                // Live events name the turns they changed; untouched rows keep their documents and closures.
                if (rebinds == null || id.isEmpty() || rebinds.contains(id) || !row.bound()) row.bind(children -> renderTurn(page, turn, key, children));
            } else renderTurn(chat, turn, key, transcriptViews);
        }
        transcriptViews.end();
        historyScroll.changed();
        if (transcriptViewport != null) transcriptViewport.dataChanged();
        updateComposer();
    }

    private void renderTurn(JSONObject page, JSONObject turn, String key, NativeKeyedViews transcriptViews) {
        JSONArray parts = array(page.optJSONObject("transcript"), "parts");
        int start = Math.max(0, turn.optInt("part_start")),
                end = Math.min(parts.length(), turn.optInt("part_end", parts.length()));
        String operatorID = turn.optString("user_id");
        if (operatorID.isEmpty()) operatorID = page.optString("user_id");
        JSONObject operator = operatorID.isEmpty() ? null : person(operatorID);
        String activityKey = turn.optString("id", page.optString("id") + ":" + turn.optInt("part_start"));
        int inputEnd = NativeTurnExchange.split(parts, start, end).get(0).inputEnd;
        String input = NativeUserRow.text(parts, start, inputEnd);
        JSONArray files = array(turn, "attachments");
        long timestamp = NativeUserRow.timestamp(parts, start, inputEnd, turn.optLong("started_at"));
        if (NativeUserRow.hasContent(input, files, parts, start, inputEnd)) {
            NativeUserRow userRow = (NativeUserRow) transcriptViews.use(key + "/user", "user",
                    () -> new NativeUserRow(ui, operator, input, timestamp, null, this::openMessageLink));
            userRow.bind(operator, input, timestamp, userFiles(page, turn, userRow, start, inputEnd, true));
            transcriptViews.use(key + "/user-gap", "", () -> ui.space(22));
        }
        NativeTurn response = (NativeTurn) transcriptViews.use(key + "/response", "", () -> new NativeTurn(ui, parts, inputEnd, end, turn,
                locale().startsWith("zh"), activityStates.computeIfAbsent(activityKey, value -> new NativeActivityList.State()), this::openMessageLink,
                transcriptImages(page)));
        response.userInput((row, from, to) -> {
            int first = inputEnd + from, last = inputEnd + to;
            String text = NativeUserRow.text(parts, first, last);
            row.setVisibility(NativeUserRow.hasContent(text, new JSONArray(), parts, first, last) ? View.VISIBLE : View.GONE);
            row.bind(operator, text, NativeUserRow.timestamp(parts, first, last, turn.optLong("started_at")), userFiles(page, turn, row, first, last, false));
        });
        response.bind(parts, inputEnd, end, turn);
        JSONObject approval = turn.optJSONObject("pending_approval");
        if (approval != null) transcriptViews.use(key + "/approval/" + approval.optString("id"),
                new JSONArray().put(approval).put(writable()), () -> approvalCard(page.optString("id"), turn.optString("id"), approval));
        if (!turn.optString("error").isEmpty() && !turn.optString("status").equals("stopped"))
            transcriptViews.use(key + "/error", turn.optString("error"), () -> ui.text(turn.optString("error"), 12, ui.error));
        transcriptViews.use(key + "/turn-gap", "", () -> ui.space(38));
    }

    private List<NativeAttachmentCards.Item> userFiles(JSONObject page, JSONObject turn, NativeUserRow row, int start, int end, boolean first) {
        JSONArray files = first ? array(turn, "attachments") : new JSONArray();
        List<NativeAttachmentCards.Item> items = new ArrayList<>();
        final int session = authEpoch, selected = selectionEpoch;
        final String ownerChat = page.optString("id", chatID);
        java.util.function.BooleanSupplier current = () -> !destroyed && foreground && user != null && session == authEpoch
                && selected == selectionEpoch && ownerChat.equals(chatID) && row.isAttachedToWindow();
        if (first && optimisticSend != null && optimisticSend.turnID.equals(turn.optString("id")) && !optimisticSend.files.isEmpty()) {
            for (Attachment file : optimisticSend.files) {
                items.add(new NativeAttachmentCards.Item(file.id, file.name, file.file.mediaType, file.excerpt, "local:" + file.id,
                        () -> { if (current.getAsBoolean()) {
                            if (NativeFileData.image(file.file.mediaType)) previewLocalImage(file);
                            else chooseDownload(file.file, file.name, session);
                        } }, image -> fileImages.local(image, file.id, file.file, Math.min(512, dp(120)), null)));
            }
        } else if (files.length() > 0) {
            for (int i = 0; i < files.length(); i++) {
                JSONObject file = at(files, i);
                if (ownerChat.isEmpty() || file.optString("id").isEmpty()) continue;
                String path = NativeFileData.attachmentPath(ownerChat, file.optString("id"));
                String name = file.optString("name"), media = file.optString("media_type");
                items.add(new NativeAttachmentCards.Item(file.optString("id"), name, media, file.optString("excerpt"), "remote:" + path,
                        () -> { if (current.getAsBoolean()) {
                            if (NativeFileData.image(media)) previewBinary(path, name); else downloadFile(path, name);
                        } }, image -> fileImages.remote(image, path, Math.min(512, dp(120)), null)));
            }
        } else {
            JSONArray parts = array(page.optJSONObject("transcript"), "parts");
            for (int i = start; i < end; i++) {
                JSONObject part = at(parts, i);
                if (!part.optString("role").equals("user")) continue;
                String source = part.optString("image_url"), name = part.optString("name", "image");
                if (part.optString("kind").equals("context") && !part.optString("name").isEmpty()) {
                    String filePath = part.optString("file_path");
                    items.add(new NativeAttachmentCards.Item(ownerChat + ":" + turn.optString("id") + ":file:" + (i - turn.optInt("part_start")), name, "application/octet-stream", "", "",
                            () -> { if (current.getAsBoolean()) { if (filePath.isEmpty()) toast(new IOException(tr("File unavailable", "文件不可用"))); else openFile(new NativeFileTarget(filePath, 0, 0)); } }, null));
                    continue;
                }
                if (!part.optString("kind").equals("image") || !NativeFileData.inlineImage(source)) continue;
                String id = ownerChat + ":" + turn.optString("id") + ":image:" + (i - turn.optInt("part_start"));
                items.add(new NativeAttachmentCards.Item(id, name, "image/png", "", source,
                        () -> { if (current.getAsBoolean()) previewInlineImage(source, name); },
                        image -> fileImages.inline(image, source, Math.min(512, dp(120)), null)));
            }
        }
        return items;
    }

    private NativeTranscriptImages transcriptImages(JSONObject page) {
        final int session = authEpoch, selected = selectionEpoch;
        final String ownerChat = page.optString("id", chatID), ownerProject = page.optString("project_id", projectID);
        return new NativeTranscriptImages() {
            private String apiPath(String path, String source) {
                if (source.matches("^/api/chats/" + java.util.regex.Pattern.quote(android.net.Uri.encode(ownerChat)) + "/tool-image\\?ref=[a-f0-9]{64}&index=\\d+$")) return source;
                NativeFileTarget target = NativeFileTarget.parse(path);
                if (target != null && !ownerProject.isEmpty() && !ownerChat.isEmpty()) return NativeFileData.filePath(ownerProject, ownerChat, target.path, "image");
                return "";
            }
            private boolean ownsChat() { return !destroyed && user != null && session == authEpoch && selected == selectionEpoch && ownerChat.equals(chatID); }
            @Override public void load(android.widget.ImageView image, String path, String source, int edge, java.util.function.Consumer<Exception> completion) {
                if (!ownsChat()) { completion.accept(new IOException("Session changed")); return; }
                if (NativeFileData.inlineImage(source)) fileImages.inline(image, source, edge, completion);
                else {
                    String api = apiPath(path, source);
                    if (api.isEmpty()) completion.accept(new IOException("Image unavailable"));
                    else fileImages.remote(image, api, edge, completion);
                }
            }
            @Override public void preview(String path, String source, String name) {
                if (!foreground || !ownsChat()) return;
                if (NativeFileData.inlineImage(source)) previewInlineImage(source, name);
                else {
                    String api = apiPath(path, source);
                    if (!api.isEmpty()) previewBinary(api, name);
                }
            }
        };
    }

    private View approvalCard(String ownerChat, String turnID, JSONObject approval) {
        final int session = authEpoch, selection = selectionEpoch;
        final String approvalID = approval.optString("id");
        LinearLayout card = ui.padded(18, 18);
        card.setBackground(ui.shape(ui.soft, 13, ui.borderStrong));
        LinearLayout heading = new LinearLayout(this); heading.setGravity(Gravity.CENTER_VERTICAL);
        android.widget.ImageView icon = ui.decorativeIcon("shield-question", 18);
        icon.setImageTintList(android.content.res.ColorStateList.valueOf(ui.primary));
        heading.addView(icon, new LinearLayout.LayoutParams(dp(18), dp(18)));
        TextView caption = ui.text(ui.t(approval.optString("kind").equals("input") ? "Your input is needed" : "Approval required"), 12, ui.primary);
        ui.typography(caption, 12, ui.primary, 600);
        LinearLayout.LayoutParams captionParams = new LinearLayout.LayoutParams(-2, -2); captionParams.setMarginStart(dp(8));
        heading.addView(caption, captionParams); card.addView(heading); gap(card, 12);
        card.addView(ui.text(ui.t(approval.optString("title")), 12, ui.text)); gap(card, 12);
        if (!approval.optString("reason").isEmpty()) { card.addView(ui.text(approval.optString("reason"), 12, ui.text)); gap(card, 12); }
        if (!approval.optString("detail").isEmpty()) {
            TextView detail = ui.text(approval.optString("detail"), 11, ui.text);
            detail.setTypeface(android.graphics.Typeface.MONOSPACE); detail.setTextIsSelectable(true);
            detail.setPadding(dp(12), dp(12), dp(12), dp(12));
            detail.setBackground(ui.shape(ui.elevated, 7, 0));
            card.addView(detail, new LinearLayout.LayoutParams(-1, -2)); gap(card, 12);
        }
        String url = approval.optString("url");
        if (!url.isEmpty()) {
            card.addView(ui.button(ui.t("Open authorization page"), false, () -> openBrowser(url)));
            card.addView(ui.text(ui.t("Complete authorization on the linked page, then confirm here."), 11, ui.muted));
        }
        String unavailable = approval.optString("unavailable");
        if (!unavailable.isEmpty()) card.addView(ui.text(ui.t(unavailable), 12, ui.muted));
        NativeApprovalForm form = new NativeApprovalForm(ui, approval);
        form.setEnabled(writable()); card.addView(form);
        if (array(approval, "decisions").length() == 0) card.addView(ui.text(ui.t("Approval applies only to this request. No permanent rules are saved."), 10, ui.muted));
        TextView decisionDetail = ui.text("", 11, ui.muted); decisionDetail.setTextIsSelectable(true); card.addView(decisionDetail);
        boolean[] sending = {false}, resolved = {false};
        NativeApprovalActions[] actions = new NativeApprovalActions[1];
        actions[0] = new NativeApprovalActions(ui, approval, decisionDetail, choice -> {
            if (sending[0] || resolved[0] || destroyed || !foreground || session != authEpoch || selection != selectionEpoch || !ownerChat.equals(chatID) || !writable()) return;
            JSONObject pending = null;
            for (int i = 0; i < array(chat, "turns").length(); i++) {
                JSONObject candidate = at(array(chat, "turns"), i);
                if (turnID.equals(candidate.optString("id"))) pending = candidate.optJSONObject("pending_approval");
            }
            if (pending == null || !approvalID.equals(pending.optString("id"))) return;
            JSONObject body;
            boolean allow = NativeApprovalActions.contentRequired(choice);
            try { body = object("id", approvalID, "turn_id", turnID, "allow", allow, "decision", choice.optString("id"), "content", allow ? form.content() : null, "feedback", form.feedback()); }
            catch (IllegalArgumentException invalid) { toast(invalid); return; }
            sending[0] = true; form.setEnabled(false); actions[0].setEnabled(false);
            api("/chats/" + ownerChat + "/approvals", body, result -> { resolved[0] = true; }, error -> {
                sending[0] = false; form.setEnabled(writable()); actions[0].setEnabled(writable()); toast(error);
            });
        });
        actions[0].setEnabled(writable());
        LinearLayout.LayoutParams actionParams = new LinearLayout.LayoutParams(-1, -2); actionParams.topMargin = dp(16);
        card.addView(actions[0], actionParams);
        return card;
    }

    private NativeHistoryLoader.Cursor historyCursor(JSONObject value) {
        JSONObject state = value == null ? null : value.optJSONObject("history");
        return new NativeHistoryLoader.Cursor(value == null || value.isNull("id") ? "" : value.optString("id"), state == null || state.isNull("before") ? "" : state.optString("before"),
                state != null && state.optBoolean("has_more"), state == null || state.isNull("error") ? "" : state.optString("error"));
    }

    private void stopHistory() { if (historyLoader != null) historyLoader.stop(); }

    private void updateHistoryLoading() {
        if (!foreground) { if (historyLoader != null) historyLoader.suspend(); return; }
        if (destroyed || user == null || client == null || chat == null || !chatID.equals(chat.optString("id")) || history == null) {
            stopHistory(); return;
        }
        if (historyLoader == null) historyLoader = new NativeHistoryLoader<>(workspaceWorker, main::post);
        final Client captured = client;
        final String auth = csrf, loc = locale();
        String key = generation + "\n" + authEpoch + "\n" + selectionEpoch + "\n" + user.optString("id") + "\n" + chatID + "\n" + loc;
        final View surface = history;
        historyLoader.bind(key, historyCursor(chat), surface, task -> NativeAfterPaint.schedule(surface, task), new NativeHistoryLoader.Source<JSONObject>() {
            public JSONObject read(NativeHistoryLoader.Cursor from) throws Exception {
                return (JSONObject) request(captured, "/chats/" + Uri.encode(from.id) + "/history?before=" + Uri.encode(from.before), null, auth, loc);
            }
            public NativeHistoryLoader.Cursor cursor(JSONObject page) { return historyCursor(page); }
        }, new NativeHistoryLoader.Listener<JSONObject>() {
            public void received(NativeHistoryLoader.Cursor from, JSONObject page) {
                if (chat == null || !from.id.equals(chat.optString("id")) || !from.before.equals(historyCursor(chat).before)) return;
                NativeHistoryScroll.Anchor anchor = historyScroll == null ? null : historyScroll.anchor();
                NativeChatState.Merge merge = NativeChatState.prependTracked(chat, page);
                chat = merge.chat; pendingRebinds = merge.changedTurns;
                renderChat(anchor);
            }
            public void failed(Exception error) {
                if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
                else renderChat();
            }
        });
    }

    private void loadEarlier() {
        if (historyLoader == null) return;
        historyLoader.retry(); renderChat();
    }

    private void adoptModel() {
        if (chat != null) { updateComposer(); return; }
        // Workspace refreshes must not reset an explicit model/effort choice.
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = at(agents, i);
            if (!agent.optBoolean("enabled")
                    || !agent.optBoolean("installed")
                    || !agent.optString("backend").equals(backend)) continue;
            JSONArray models = array(agent.optJSONObject("catalog"), "models");
            for (int j = 0; j < models.length(); j++) {
                if (at(models, j).optString("model").equals(model)) {
                    updateComposer();
                    return;
                }
            }
        }
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = at(agents, i);
            if (!agent.optBoolean("enabled") || !agent.optBoolean("installed")) continue;
            if (!backend.equals(agent.optString("backend"))) {
                permission = "";
                invalidatePermissions();
            }
            backend = agent.optString("backend", "codex");
            JSONObject catalog = agent.optJSONObject("catalog");
            if (catalog != null) {
                NativeModelSelection selection = NativeModelSelection.choose(agent, catalog.optString("defaultModel"));
                model = selection.model;
                effort = selection.effort;
            }
            updateComposer();
            loadPermissions();
            return;
        }
    }

    private void chooseModel() {
        showModelMenu();
    }

    private void showModelMenu() {
        NativePopover menu = NativePopover.model(ui);
        NativeAgentModelMenu[] content = new NativeAgentModelMenu[1];
        content[0] = new NativeAgentModelMenu(ui, agents,
                new NativeModelSelection(backend, model, effort), chat == null ? "" : chat.optString("backend"), next -> {
                    if (chat != null && !canCreateInProject() && !next.backend.equals(chat.optString("backend"))) {
                        NativeModelSelection original = NativeModelSelection.chat(agents, chat);
                        backend = original.backend;
                        model = original.model;
                        effort = original.effort;
                        content[0].bind(original);
                        updateComposer();
                        return;
                    }
                    boolean changedBackend = !backend.equals(next.backend);
                    backend = next.backend;
                    model = next.model;
                    effort = next.effort;
                    if (changedBackend) {
                        invalidatePermissions();
                        permission = "";
                        loadPermissions();
                    }
                    updateComposer();
                });
        menu.content.addView(content[0], new LinearLayout.LayoutParams(-1, -2));
        modelMenu = content[0];
        modelPopover = menu;
        menu.show(modelButton, true);
    }

    private boolean canCreateInProject() {
        for (int i = 0; i < projects.length(); i++) {
            JSONObject project = at(projects, i);
            if (project.optString("id").equals(projectID)) return project.optBoolean("can_create", true);
        }
        return false;
    }

    private boolean selectedAgentAvailable() {
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = at(agents, i);
            if (agent.optString("backend").equals(backend)) return agent.optBoolean("enabled") && agent.optBoolean("installed");
        }
        return false;
    }

    private String permissionLabel(String mode) {
        return NativePermissionMenu.label(ui, backend, mode);
    }

    private void invalidatePermissions() {
        permissionCatalog = null;
        permissionLoading = permissionFailed = false;
        ++permissionRequest;
        if (permissionMenu != null) permissionMenu.popover.dismiss();
        permissionMenu = null;
    }

    private void loadPermissions() {
        if (projectID.isEmpty() || !writable() || user == null) return;
        int selected = selectionEpoch, request = ++permissionRequest;
        String selectedBackend = backend;
        permissionCatalog = null;
        permissionLoading = true;
        permissionFailed = false;
        updatePermissionMenu();
        updateComposer();
        api(
                "/permissions?project_id="
                        + Uri.encode(projectID)
                        + "&backend="
                        + Uri.encode(backend)
                        + (chatID.isEmpty() ? "" : "&chat_id=" + Uri.encode(chatID)),
                null,
                value -> {
                    if (request != permissionRequest || selected != selectionEpoch || !selectedBackend.equals(backend)) return;
                    permissionCatalog = value instanceof JSONObject ? (JSONObject) value : new JSONObject();
                    if (permission.isEmpty()) permission = permissionCatalog.optString("current");
                    if (permission.isEmpty()) permission = permissionCatalog.optString("default");
                    if (permission.isEmpty()) permission = backend.equals("claude") ? "acceptEdits" : "ask";
                    permissionLoading = false;
                    permissionFailed = false;
                    updatePermissionMenu();
                    updateComposer();
                }, error -> {
                    if (request != permissionRequest || selected != selectionEpoch || !selectedBackend.equals(backend)) return;
                    permissionLoading = false;
                    permissionFailed = true;
                    updatePermissionMenu();
                    updateComposer();
                });
    }

    private boolean permissionReady() {
        for (int i = 0; i < array(permissionCatalog, "modes").length(); i++) {
            JSONObject mode = at(array(permissionCatalog, "modes"), i);
            if (mode.optString("id").equals(permission)) return mode.optBoolean("available");
        }
        return false;
    }

    private void updatePermissionMenu() {
        if (permissionMenu != null && permissionMenu.popover.isShowing())
            permissionMenu.bind(permission, permissionCatalog, permissionLoading, permissionFailed);
    }

    private void choosePermission() {
        if (permissionMenu != null && permissionMenu.popover.isShowing()) { permissionMenu.popover.dismiss(); return; }
        if (permissionCatalog == null && !permissionLoading && !permissionFailed) loadPermissions();
        String selectedBackend = backend;
        int selected = selectionEpoch;
        permissionMenu = new NativePermissionMenu(ui, backend, permission, permissionCatalog, permissionLoading, permissionFailed, id -> {
            if (selected != selectionEpoch || !selectedBackend.equals(backend)) return;
            permission = id;
            updateComposer();
        }, this::loadPermissions);
        permissionMenu.show(permissionButton);
    }

    private void sendOrStop() {
        if (!writable()) return;
        JSONObject activeTurn = NativeChatState.runningTurn(chat);
        if (activeTurn != null) { stopResponse(activeTurn); return; }
        if (busy || chat != null && chat.optString("status").equals("running")) return;
        if (!selectedAgentAvailable()) return;
        captureDraft();
        final Draft submitted = draft();
        if (NativeGoalCommand.trim(submitted.text).isEmpty() && submitted.files.isEmpty()) return;
        if (!permissionReady()) {
            toast(new IOException(tr("Check project permissions before sending.", "请先检查项目权限。")));
            loadPermissions();
            return;
        }
        NativeGoalCommand command = NativeGoalCommand.parse(submitted.text);
        if (command != null) {
            String issue = command.validationError(!submitted.files.isEmpty());
            if (!issue.isEmpty()) { goalError = ui.t(issue); updateComposer(); return; }
            if (!command.action.equals("set")) { runGoalControl(command.action, submitted); return; }
        }
        goalNotice = goalError = "";
        final String pid = projectID,
                initialID = chatID,
                auth = csrf,
                loc = locale();
        final Client captured = client;
        final int token = generation, session = authEpoch;
        final NativeSubmission<Attachment> sending = new NativeSubmission<>(session, selectionKey(), initialID, submitted.text, submitted.files);
        JSONObject payload =
                object(
                        "message",
                        sending.message,
                        "backend",
                        backend,
                        "model",
                        model,
                        "reasoning_effort",
                        effort,
                        "permission_mode",
                        permission);
        final List<Attachment> files = sending.files;
        final String agent = backend;
        pendingSend = optimisticSend = sending;
        JSONArray attached = new JSONArray();
        for (Attachment file : files) {
            attached.put(object("id", file.id, "name", file.name, "media_type", file.file.mediaType, "size", file.data.length, "excerpt", file.excerpt));
        }
        JSONObject turn = object("id", sending.turnID, "user_id", user == null ? "" : user.optString("id"), "backend", agent,
                "requested_model", model, "reasoning_effort", effort, "permission_mode", permission,
                "attachments", attached, "started_at", System.currentTimeMillis(), "status", "running");
        JSONObject optimistic = NativeChatState.optimistic(chat, pid, turn, NativeChatState.input(sending.message, attached));
        if (chat == null) NativeChatState.put(optimistic, "can_control", true);
        chat = optimistic;
        busy = true;
        submitted.text = ""; submitted.files.clear(); composer.setText("");
        if (modelPopover != null) modelPopover.dismiss();
        if (permissionMenu != null) permissionMenu.popover.dismiss();
        closeImagePreview(); renderFiles(); renderChat();
        io.execute(
                () -> {
                    String id = initialID;
                    try {
                        if (token != generation || session != authEpoch || destroyed) return;
                        if (id.isEmpty()) {
                            final JSONObject created = (JSONObject) request(captured, "/chats",
                                    object("project_id", pid, "backend", agent), auth, loc);
                            id = created.optString("id");
                            if (id.isEmpty()) throw new IOException(tr("The server returned an invalid response", "服务器返回了无效响应"));
                            post(token, () -> {
                                if (session != authEpoch || pendingSend != sending) return;
                                boolean current = sending.current(authEpoch, selectionKey());
                                sending.adopt(created.optString("id"));
                                if (current) {
                                    captureDraft();
                                    chatID = created.optString("id"); drafts.put(chatID, submitted);
                                    drafts.remove("new/" + pid);
                                    ++selectionEpoch;
                                    chat = NativeChatState.adoptCreated(chat, created);
                                    NativeChatState.put(chat, "can_control", created.optBoolean("can_control", true));
                                    renderChat(); startChat();
                                }
                            });
                        }
                        if (token != generation || session != authEpoch || destroyed) return;
                        Object value;
                        if (files.isEmpty())
                            value =
                                    request(
                                            captured,
                                            "/chats/" + id + "/messages",
                                            payload,
                                            auth,
                                            loc);
                        else {
                            JSONArray upload = new JSONArray();
                            for (Attachment file : files)
                                upload.put(
                                        object(
                                                "name",
                                                file.name,
                                                "data",
                                                android.util.Base64.encodeToString(
                                                        file.data, android.util.Base64.NO_WRAP)));
                            value =
                                    decodeResponse(
                                            captured.upload(
                                                    id,
                                                    object("fields", payload, "files", upload)
                                                            .toString(),
                                                    auth,
                                                    loc));
                        }
                        final JSONObject result = (JSONObject) value;
                        if (result.optString("id").isEmpty()) throw new IOException(tr("The server returned an invalid response", "服务器返回了无效响应"));
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch || pendingSend != sending) return;
                                    busy = false;
                                    boolean current = sending.current(authEpoch, selectionKey());
                                    if (current) {
                                        captureDraft();
                                        Draft currentDraft = draft();
                                        if (!chatID.equals(result.optString("id"))) {
                                            drafts.remove(sending.key);
                                            chatID = result.optString("id");
                                            sending.adopt(chatID); ++selectionEpoch;
                                        }
                                        chat = NativeChatState.reconcile(chat, result);
                                        drafts.put(chatID, currentDraft);
                                    }
                                    pendingSend = null;
                                    if (sending.stopRequested) stopAccepted(result, sending.turnID);
                                    if (current) { renderFiles(); renderChat(); startChat(); }
                                    else updateComposer();
                                    refreshWorkspace();
                                });
                    } catch (Exception e) {
                        final String createdID = id;
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch || pendingSend != sending) return;
                                    busy = false;
                                    pendingSend = null; stoppingTurns.remove(sending.turnID);
                                    if (e instanceof ApiFailure && ((ApiFailure) e).status == 401) {
                                        expire();
                                        return;
                                    }
                                    if (sending.current(authEpoch, selectionKey())) {
                                        // Web restores the submitted draft, including exact whitespace and attachments.
                                        Draft restored = draft();
                                        restored.text = sending.message;
                                        restored.files.clear(); restored.files.addAll(files);
                                        composer.setText(restored.text);
                                        goalError = e.getMessage() == null ? e.toString() : e.getMessage();
                                        if (createdID.isEmpty()) chat = null;
                                        else {
                                            // Keep any live SSE state until the authoritative recovery read finishes.
                                            chatID = createdID; drafts.put(chatID, restored);
                                            final String target = createdID;
                                            final int selected = selectionEpoch;
                                            api("/chats/" + Uri.encode(target), null, value -> {
                                                if (selected != selectionEpoch || !target.equals(chatID)) return;
                                                chat = (JSONObject) value; renderChat();
                                            }, ignored -> {});
                                        }
                                        renderFiles(); renderChat();
                                    }
                                    updateComposer();
                                });
                    }
                });
    }

    private NativeSubmission<Attachment> activeSubmission(JSONObject turn) {
        return pendingSend != null && (pendingSend.turnID.equals(turn.optString("id")) || pendingSend.chatID.equals(chatID)) ? pendingSend : null;
    }

    private void stopResponse(JSONObject turn) {
        String id = turn.optString("id");
        if (stoppingTurns.contains(id)) return;
        NativeSubmission<Attachment> pending = activeSubmission(turn);
        goalError = "";
        if (pending != null) {
            if (!pending.requestStop()) return;
            stoppingTurns.add(id); stoppingTurns.add(pending.turnID); updateComposer();
        } else stopAccepted(chat, id);
    }

    private void stopAccepted(JSONObject accepted, String pendingID) {
        JSONObject turn = NativeChatState.runningTurn(accepted);
        if (turn == null || turn.optString("id").startsWith("pending-")) { stoppingTurns.remove(pendingID); updateComposer(); return; }
        String turnID = turn.optString("id"), target = accepted.optString("id");
        stoppingTurns.add(pendingID); stoppingTurns.add(turnID); updateComposer();
        api("/chats/" + Uri.encode(target) + "/stop", object("turn_id", turnID), value -> {
            stoppingTurns.remove(pendingID); stoppingTurns.remove(turnID);
            chats = NativeChatState.reconcileList(chats, (JSONObject) value);
            if (target.equals(chatID)) { chat = NativeChatState.reconcile(chat, (JSONObject) value); renderChat(); }
            updateComposer();
        }, failure -> {
            stoppingTurns.remove(pendingID); stoppingTurns.remove(turnID);
            if (target.equals(chatID)) goalError = failure.getMessage() == null ? failure.toString() : failure.getMessage();
            updateComposer();
        });
    }

    private void runGoalControl(String action, Draft submitted) {
        if (chat != null && !backend.equals(chat.optString("backend"))) {
            goalError = ui.t("Select this chat's current agent to view or clear its goal"); updateComposer(); return;
        }
        NativeGoalRequest request = new NativeGoalRequest(authEpoch, selectionEpoch, selectionKey(), submitted.text);
        goalNotice = goalError = "";
        if (chatID.isEmpty()) {
            goalNotice = ui.t("No goal set."); submitted.text = ""; composer.setText(""); updateComposer(); return;
        }
        busy = true; updateComposer();
        api("/chats/" + Uri.encode(chatID) + "/goal", object("action", action), value -> {
            busy = false;
            if (request.current(authEpoch, selectionEpoch, selectionKey())) {
                goalNotice = ((JSONObject) value).optString("message");
                if (request.clears(submitted.text)) submitted.text = "";
                if (composer != null && request.clears(composer.getText().toString())) composer.setText("");
            }
            updateComposer();
        }, failure -> {
            busy = false;
            if (request.current(authEpoch, selectionEpoch, selectionKey())) goalError = failure.getMessage() == null ? failure.toString() : failure.getMessage();
            updateComposer();
        });
    }

    private void pickFiles() {
        if (!writable() || busy) return;
        captureDraft();
        pickerDraft = draft();
        Intent intent =
                new Intent(Intent.ACTION_OPEN_DOCUMENT)
                        .addCategory(Intent.CATEGORY_OPENABLE)
                        .setType("*/*")
                        .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        try {
            startActivityForResult(
                    Intent.createChooser(intent, tr("Choose attachments", "选择附件")), PICK_FILES);
        } catch (ActivityNotFoundException e) {
            toast(e);
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == NativeAppUpdate.INSTALL_PERMISSION) { appUpdates.permissionResult(this); return; }
        if (requestCode == SAVE_FILE) {
            PendingDownload selected = pendingDownload;
            pendingDownload = null;
            downloading = false;
            if (selected == null || selected.session != authEpoch || resultCode != RESULT_OK || data == null || data.getData() == null) return;
            Uri destination = data.getData();
            if (!"content".equals(destination.getScheme())) { toast(new IOException("Only document-provider destinations are supported")); return; }
            int token = generation;
            io.execute(() -> {
                try (OutputStream output = getContentResolver().openOutputStream(destination, "wt")) {
                    if (output == null) throw new IOException("Cannot write file");
                    output.write(selected.file.bytes);
                } catch (Exception error) { post(token, () -> { if (selected.session == authEpoch) toast(error); }); }
            });
            return;
        }
        if (requestCode != PICK_FILES) return;
        boolean selecting = pickerDraft != null;
        pickerDraft = null;
        if (resultCode != RESULT_OK || data == null || !selecting) return;
        final String key = selectionKey();
        final String selectedProject = projectID;
        final int version = draftVersion;
        List<Uri> uris = new ArrayList<>();
        ClipData clips = data.getClipData();
        if (clips != null)
            for (int i = 0; i < clips.getItemCount() && i < 6; i++)
                uris.add(clips.getItemAt(i).getUri());
        else if (data.getData() != null) uris.add(data.getData());
        final int token = generation, session = authEpoch;
        io.execute(
                () -> {
                    List<Attachment> picked = new ArrayList<>();
                    try {
                        for (Uri uri : uris) {
                            if (!"content".equals(uri.getScheme()))
                                throw new IOException(
                                        "Only document-provider attachments are supported");
                            String name = "attachment";
                            try (Cursor cursor =
                                    getContentResolver()
                                            .query(
                                                    uri,
                                                    new String[] {OpenableColumns.DISPLAY_NAME},
                                                    null,
                                                    null,
                                                    null)) {
                                if (cursor != null && cursor.moveToFirst())
                                    name = cursor.getString(0);
                            }
                            try (InputStream in = getContentResolver().openInputStream(uri);
                                    ByteArrayOutputStream out = new ByteArrayOutputStream()) {
                                if (in == null) throw new IOException("Cannot read attachment");
                                byte[] buffer = new byte[8192];
                                int n;
                                while ((n = in.read(buffer)) != -1) {
                                    if (out.size() + n > 4 * 1024 * 1024)
                                        throw new IOException(
                                                tr(
                                                        "Each attachment must be at most 4 MB",
                                                        "每个附件不能超过 4 MB"));
                                    out.write(buffer, 0, n);
                                }
                                if (out.size() == 0)
                                    throw new IOException(tr("Empty attachment", "附件为空"));
                                String mediaType = getContentResolver().getType(uri);
                                if (mediaType == null || mediaType.isEmpty()) {
                                    mediaType = java.net.URLConnection.guessContentTypeFromName(name);
                                }
                                picked.add(new Attachment(name, mediaType, out.toByteArray()));
                            }
                        }
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch || version != draftVersion
                                            || !selectedProject.equals(projectID) || !key.equals(selectionKey())) return;
                                    Draft target = draft();
                                    int bytes = 0;
                                    for (Attachment file : target.files) bytes += file.data.length;
                                    for (Attachment file : picked) bytes += file.data.length;
                                    if (target.files.size() + picked.size() > 6
                                            || bytes > 16 * 1024 * 1024) {
                                        toast(
                                                new IOException(
                                                        tr(
                                                                "Attach at most 6 files, up to 16"
                                                                        + " MB total",
                                                                "最多 6 个附件，总计不超过 16 MB")));
                                        return;
                                    }
                                    target.files.addAll(picked);
                                    renderFiles();
                                });
                    } catch (Exception e) {
                        post(token, () -> toast(e));
                    }
                });
    }

    private void renderFiles() {
        if (composer == null || fileBar == null || composerView == null) return;
        fileBar.removeAllViews();
        Draft owner = draft();
        for (Attachment file : new ArrayList<>(owner.files)) {
            NativeAttachmentCard card = new NativeAttachmentCard(ui, file.name, file.file.mediaType, file.excerpt, null,
                    NativeFileData.image(file.file.mediaType) ? () -> previewLocalImage(file) : null, () -> {
                if (!busy) {
                    owner.files.remove(file);
                    if (owner == draft()) renderFiles();
                }
            });
            if (card.remove != null) card.remove.setEnabled(!busy);
            fileBar.addView(card);
            if (card.image != null) fileImages.local(card.image, file.id, file.file, Math.min(512, dp(120)), null);
        }
        composerView.attachmentScroll.setVisibility(draft().files.isEmpty() ? View.GONE : View.VISIBLE);
        updateComposer();
    }

    private void stopChat() {
        if (historyLoader != null) historyLoader.suspend();
        if (chatEvents != null) chatEvents.stop();
    }

    private void stopStreams() {
        if (workspaceData != null) workspaceData.stop();
        if (chatRefresh != null) chatRefresh.stop();
        ++workspaceEpoch;
        if (workspaceEvents != null) workspaceEvents.stop();
        else closeStream(workspaceStream);
        workspaceStream = null;
        setWorkspaceConnected(false);
        stopChat();
    }

    private void closeStream(EventStream stream) {
        if (stream == null) return;
        try { streamCloser.execute(stream::close); }
        catch (java.util.concurrent.RejectedExecutionException ignored) {
            // A handle returned after Activity teardown still has to be released.
            stream.close();
        }
    }

    private void suspendStreams() {
        if (workspaceData != null) workspaceData.stop();
        if (chatRefresh != null) chatRefresh.stop();
        stopChat();
        if (workspaceEvents != null) workspaceEvents.visibility(false);
    }

    private void startChat() {
        updateChatRefresh();
        if (sidebarPageDeferred || routeLoginRequired || !foreground || destroyed || client == null || user == null || chatID.isEmpty()) { stopChat(); return; }
        if (chatEvents == null) chatEvents = new NativeChatEvents<>(io, new NativeChatRefresh.Dispatcher() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
        });
        final String id = chatID, auth = csrf;
        final Client captured = client;
        String key = generation + "\n" + authEpoch + "\n" + user.optString("id") + "\n" + id;
        chatEvents.bind(key, new NativeChatEvents.Source<JSONObject>() {
            public Runnable open(NativeChatEvents.Events<JSONObject> events) throws Exception {
                EventStream stream = captured.subscribeChat(id, locale(), (kind, raw) -> {
                    if (kind.equals("transport_error")) {
                        main.post(() -> { if (client == captured && chatID.equals(id)) connectionFailed(captured); });
                        events.error(); return;
                    }
                    if (!kind.equals("snapshot") && !kind.equals("metadata") && !kind.equals("turn")) return;
                    try {
                        JSONObject value = new JSONObject(raw);
                        if (kind.equals("snapshot") && !id.equals(value.optString("id"))) return;
                        events.event(kind, value);
                    } catch (Exception ignored) { /* An invalid frame is not a chat snapshot. */ }
                });
                return () -> closeStream(stream);
            }
            public JSONObject snapshot() throws Exception {
                JSONObject value = (JSONObject) request(captured, "/chats/" + Uri.encode(id), null, auth, locale());
                if (!id.equals(value.optString("id"))) throw new IOException("Unexpected chat in initialization response");
                return value;
            }
            public void checkAuth() throws Exception { request(captured, "/me", null, auth, locale()); }
        }, new NativeChatEvents.Listener<JSONObject>() {
            public void event(String kind, JSONObject value) {
                try { receiveChatEvent(kind, value); }
                catch (Exception error) { toast(error); }
            }
            public void connected(boolean value) { /* Ordinary headers intentionally contain no connection diagnostics. */ }
            public void failed(Exception error) {
                if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) { expire(); return; }
                goalError = error.getMessage() == null ? error.toString() : error.getMessage();
                updateComposer();
            }
            public void authFailed(Exception error) {
                if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
            }
        });
    }

    private void receiveChatEvent(String kind, JSONObject data) throws Exception {
        JSONObject previous = chat;
        double previousRevision = previous == null ? -1 : previous.optDouble("updated_at", 0);
        boolean initial = previous == null;
        NativeChatState.Merge merge = NativeChatState.eventTracked(chat, kind, data);
        // An unchanged frame keeps the current state object, like an unchanged poll.
        chat = merge.unchanged ? chat : merge.chat;
        if (fileBrowser != null && chat != null && previousRevision != chat.optDouble("updated_at", 0)) fileBrowser.refreshWorkspace();
        chats = NativeChatState.eventList(chats, chatID, kind, data);
        if (chat == null) { renderNavigation(); return; }
        if (initial) {
            projectID = chat.optString("project_id"); sidebarState.expanded.add(projectID);
            NativeModelSelection selection = NativeModelSelection.chat(agents, chat);
            backend = selection.backend; model = selection.model; effort = selection.effort;
            permission = chat.optString("permission_mode");
            renderFiles(); loadPermissions();
        }
        if (!merge.unchanged) { pendingRebinds = merge.changedTurns; renderChat(); }
        else updateChatRefresh();
        renderNavigation();
    }

    private void updateChatRefresh() {
        if (sidebarPageDeferred || !foreground || destroyed || client == null || user == null || chatID.isEmpty()
                || chat == null || !chatID.equals(chat.optString("id")) || !NativeChatState.hasRunningTurn(chat)) {
            if (chatRefresh != null) chatRefresh.stop();
            return;
        }
        if (chatRefresh == null) chatRefresh = new NativeChatRefresh<>(io, new NativeChatRefresh.Dispatcher() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
        });
        final String id = chatID, auth = csrf;
        final Client captured = client;
        String key = generation + "\n" + authEpoch + "\n" + user.optString("id") + "\n" + id;
        chatRefresh.bind(key, () -> {
            JSONObject value = (JSONObject) request(captured, "/chats/" + Uri.encode(id), null, auth, locale());
            if (!id.equals(value.optString("id"))) throw new IOException("Unexpected chat in refresh response");
            return value;
        }, new NativeChatRefresh.Listener<JSONObject>() {
            public void received(JSONObject next) {
                if (chat == null || !id.equals(chatID) || !id.equals(chat.optString("id"))) return;
                NativeChatState.Merge merge = NativeChatState.reconcileTracked(chat, next);
                chats = NativeChatState.reconcileList(chats, next);
                // React retains keyed transcript nodes for an unchanged poll; do not rebuild native selection/Views either.
                if (!merge.unchanged) { chat = merge.chat; pendingRebinds = merge.changedTurns; renderChat(); }
                else { updateChatRefresh(); renderNavigation(); }
            }
            public void failed(Exception error) {
                if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
            }
        });
    }

    private boolean startWorkspace() {
        if (routeLoginRequired || !foreground || destroyed || user == null || client == null) return false;
        final Client captured = client;
        final String auth = csrf;
        String key = generation + "\n" + authEpoch + "\n" + user.optString("id");
        if (!presenceScope.equals(key)) {
            presenceScope = key;
            presence = new JSONArray(); presenceStale = true;
            updateHeaderPeople(); updateMissingProfiles();
        }
        NativeWorkspaceEvents.Listener<EventStream> listener = new NativeWorkspaceEvents.Listener<EventStream>() {
            public void connection(EventStream stream) { workspaceStream = stream; ++workspaceEpoch; }
            public void event(String kind, String value) { decodeWorkspaceEvent(kind, value, workspaceEpoch); }
            public void connected(boolean value) { setWorkspaceConnected(value); }
            public void failed() { if (client == captured) connectionFailed(captured); }
            public void expired() { expire(); }
            public void authFailed(Exception error) {
                if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
            }
        };
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        ServerFeed feed = runtime == null ? null : runtime.feed;
        if (feed != null && feed.client == captured && feed.userID.equals(user.optString("id")) && feed.csrf.equals(auth)) {
            runtime.feed = null;
            if (feed.data != null) feed.data.stop();
            if (workspaceEvents != null && workspaceEvents != feed.events) workspaceEvents.stop();
            workspaceEvents = feed.events;
            if (workspaceEvents != null && workspaceEvents.handoff(key, listener)) {
                presence = feed.presence; presenceStale = !workspaceEvents.ready();
                setDesktopAvailable(workspaceEvents.ready() && feed.desktopAvailable);
                workspaceEvents.visibility(true);
                updateHeaderPeople();
                return true;
            }
        } else if (feed != null) stopServerFeed(runtime);
        if (workspaceEvents == null) workspaceEvents = new NativeWorkspaceEvents<>(workspaceSocketWorker, workspaceProbeWorker, new NativeWorkspaceEvents.Scheduler() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
            // Resume must count deep sleep even before paused Handler timers can fire.
            public long now() { return android.os.SystemClock.elapsedRealtime(); }
            public double random() { return Math.random(); }
        });
        boolean bound = workspaceEvents.bind(key, new NativeWorkspaceEvents.Source<EventStream>() {
            public EventStream open(NativeWorkspaceEvents.Events events) throws Exception {
                return captured.subscribeWorkspace(auth, locale(), events::event);
            }
            public void close(EventStream stream) { closeStream(stream); }
            public void send(EventStream stream, String type, boolean visible) throws Exception {
                stream.send(object("type", type, "visible", visible).toString());
            }
            public void probe(EventStream stream) throws Exception { stream.probe(); }
            public void checkAuth() throws Exception { request(captured, "/me", null, auth, locale()); }
        }, listener);
        if (bound) setWorkspaceConnected(false);
        return bound;
    }

    private void setWorkspaceConnected(boolean value) {
        workspaceReady = value;
        if (workspaceData != null) workspaceData.ready(value);
        if (!value) {
            setDesktopAvailable(false);
            presenceStale = true;
            disconnectPeople(); updateHeaderPeople(); updateMissingProfiles();
        } else renderPeople();
    }

    private void resumeWorkspace() {
        resumeWorkspace(false);
    }

    private void resumeWorkspace(boolean returning) {
        if (routeLoginRequired || !foreground || destroyed || user == null || client == null) return;
        if (fileBrowser != null) fileBrowser.resume();
        boolean bound = startWorkspace();
        workspaceEvents.visibility(true);
        if (!bound) workspaceEvents.resume(returning);
        if (!workspaceReady) refreshWorkspace();
        if (!inbox.active.isEmpty()) { inbox.open(inbox.active, profiles); updateHeaderPeople(); renderPeople(); }
    }

    private void workspaceEvent(String kind, String raw, int epoch) throws Exception {
        if (workspaceEpoch != epoch || user == null) return;
        if (kind.equals("ready")) { setWorkspaceConnected(true); return; }
        if (kind.equals("transport_error")) { if (workspaceEvents != null) workspaceEvents.failed(); return; }
        if (kind.equals("auth_expired")) { expire(); return; }
        if (!java.util.Arrays.asList("navigation", "agents", "profiles", "presence", "chat_groups", "direct_message", "group_message", "direct_message_error", "group_result", "desktop").contains(kind)) return;
        Object value = new JSONTokener(raw).nextValue();
        applyWorkspaceEvent(kind, value, epoch);
    }

    private void decodeWorkspaceEvent(String kind, String raw, int epoch) {
        if (kind.equals("ready") || kind.equals("transport_error") || kind.equals("auth_expired")) {
            try { workspaceEvent(kind, raw, epoch); } catch (Exception error) { toast(error); }
            return;
        }
        if (!java.util.Arrays.asList("navigation", "agents", "profiles", "presence", "chat_groups", "direct_message", "group_message", "direct_message_error", "group_result", "desktop").contains(kind)) return;
        final int token = generation, session = authEpoch;
        final ServerProfiles.Server server = serverProfiles.active();
        final Client sourceClient = client;
        final NativeWorkspaceEvents<EventStream> source = workspaceEvents;
        final EventStream stream = workspaceStream;
        final String account = user == null ? "" : user.optString("id"), auth = csrf;
        workspaceDecode.execute(() -> {
            if (destroyed) return;
            try {
                Object value = new JSONTokener(raw).nextValue();
                main.post(() -> {
                    if (token == generation && session == authEpoch && epoch == workspaceEpoch && !destroyed) {
                        try { applyWorkspaceEvent(kind, value, epoch); } catch (Exception error) { toast(error); }
                    } else routeServerEvent(server, sourceClient, source, stream, account, auth, kind, value);
                });
            } catch (Exception error) {
                post(token, () -> { if (session == authEpoch && epoch == workspaceEpoch) toast(error); });
            }
        });
    }

    private void applyWorkspaceEvent(String kind, Object value, int epoch) throws Exception {
        if (destroyed || workspaceEpoch != epoch || user == null) return;
        switch (kind) {
            case "desktop":
                setDesktopAvailable(((JSONObject) value).optBoolean("available"));
                break;
            case "navigation":
                if (workspaceData != null && foreground) workspaceData.navigation((JSONObject) value);
                else acceptNavigation((JSONObject) value);
                break;
            case "agents":
                acceptAgents((JSONArray) value);
                break;
            case "profiles":
                acceptProfiles((JSONObject) value);
                refreshProfileViews();
                break;
            case "presence":
                presence = (JSONArray) value;
                presenceStale = false;
                updateHeaderPeople();
                renderPeople();
                updateMissingProfiles();
                break;
            case "chat_groups":
                groups = (JSONArray) value;
                inbox.groups(groups);
                renderPeople();
                break;
            case "direct_message":
            case "group_message": {
                JSONObject message = (JSONObject) value;
                inbox.acknowledge(message);
                inbox.receive(message, user.optString("id"), foreground && peoplePanel != null, profiles);
                updateHeaderPeople();
                renderPeople();
                break;
            }
            case "direct_message_error": {
                JSONObject error = (JSONObject) value;
                failPeople(error.optString("request_id"), error.optString("code"));
                break;
            }
            case "group_result": {
                JSONObject result = (JSONObject) value;
                NativeGroupPicker.Completion completion = pendingGroups.remove(result.optString("request_id"));
                if (completion != null) completion.done(result.optJSONObject("group"), "");
                break;
            }
            default:
                break;
        }
    }

    private String personName(String id) {
        if (user != null && user.optString("id").equals(id))
            return user.optString("nickname", user.optString("username"));
        if (id.equals("online-room")) return tr("Online group chat", "在线群聊");
        for (int i = 0; i < groups.length(); i++)
            if (at(groups, i).optString("id").equals(id)) return at(groups, i).optString("name");
        for (int i = 0; i < profiles.length(); i++)
            if (at(profiles, i).optString("id").equals(id))
                return at(profiles, i).optString("nickname");
        return tr("Workspace member", "工作空间成员");
    }

    private JSONObject person(String id) {
        if (id.equals("spool-bot")) return object("id", id, "nickname", "Spool Bot", "bot", true);
        for (int i = 0; i < profiles.length(); i++)
            if (at(profiles, i).optString("id").equals(id)) return at(profiles, i);
        if (user != null && user.optString("id").equals(id)) return user;
        for (int i = 0; i < presence.length(); i++)
            if (at(presence, i).optString("id").equals(id)) return at(presence, i);
        return object("id", id, "nickname", id);
    }

    private void acceptProfiles(JSONObject snapshot) throws Exception {
        if (!profileCache.accept(snapshot)) return;
        updateOwnProfile();
        renderNavigation();
        updateMissingProfiles();
    }

    private void updateOwnProfile() throws Exception {
        profiles = profileCache.people;
        if (user != null) {
            for (int i = 0; i < profiles.length(); i++) {
                JSONObject profile = at(profiles, i);
                if (!profile.optString("id").equals(user.optString("id"))) continue;
                user.put("nickname", profile.optString("nickname"));
                user.put("avatar", profile.optJSONObject("avatar"));
                rememberAccount();
                break;
            }
        }
    }

    private void closeAvatarPicker() {
        if (avatarDialog != null) avatarDialog.dismiss();
        avatarDialog = null;
    }

    private void openAvatarPicker() {
        if (user == null || avatarDialog != null) return;
        final int token = generation, session = authEpoch;
        final Client captured = client;
        final String auth = csrf, loc = locale();
        avatarDialog = new NativeAvatarPicker(ui, person(user.optString("id")), (avatar, completion) -> {
            io.execute(() -> {
                try {
                    JSONObject result = (JSONObject) request(captured, "/me/avatar", avatar, auth, loc);
                    post(token, () -> {
                        if (session != authEpoch || user == null) return;
                        try {
                            profileCache.acceptSaved(result.getJSONObject("profiles"), user.optString("id"));
                            updateOwnProfile();
                            completion.accept(null);
                            render();
                        } catch (Exception error) {
                            completion.accept(error.getMessage());
                        }
                    });
                } catch (Exception error) {
                    post(token, () -> {
                        if (session != authEpoch) return;
                        if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
                        else completion.accept(error.getMessage() == null
                                ? tr("Could not save avatar", "无法保存头像") : error.getMessage());
                    });
                }
            });
        }, this::openMessageLink);
        avatarDialog.setOnDismissListener(dialog -> avatarDialog = null);
        avatarDialog.show();
    }

    private void updateHeaderPeople() {
        if (workspaceHeader == null) return;
        List<JSONObject> people = new ArrayList<>();
        for (int i = 0; i < presence.length(); i++) people.add(person(at(presence, i).optString("id")));
        workspaceHeader.bindPeople(people, inbox.unreadCount());
    }

    private void openPeople() {
        openPeople(inbox.initial(user.optString("id")));
    }

    private void openOnlinePeople() {
        if (user == null || workspaceHeader == null) return;
        if (peopleMenu != null && peopleMenu.isShowing()) { peopleMenu.dismiss(); return; }
        peopleMenu = new NativePeopleMenu(ui, this::openPeople, this::openPeople, () -> openGroupPicker(""));
        peopleMenu.bind(user.optString("id"), presence, inbox, this::peopleIdentity);
        peopleMenu.show(workspaceHeader.online);
    }

    private void openPeople(String id) {
        if (user == null) return;
        ui.dismissMenus();
        closeDrawer();
        inbox.open(id, profiles);
        if (peoplePanel == null) {
            peoplePanel = new NativePeoplePanel(ui, inbox, this::peopleIdentity, peopleBounds, value -> peopleBounds = value,
                    this::openPeople, text -> inbox.draft(inbox.active, text), this::sendPeople, this::closePeople,
                    () -> openGroupPicker(""), () -> openGroupPicker(inbox.active));
            root.addView(peoplePanel, new FrameLayout.LayoutParams(-1, -1));
        }
        renderPeople();
        updateHeaderPeople();
    }

    private JSONObject peopleIdentity(String id) {
        if (NativePeopleModel.group(id)) {
            String name = id.equals(NativePeopleModel.ROOM) ? ui.t("Online group chat") : inbox.groupNames.getOrDefault(id, "Group chat");
            return object("id", id, "nickname", name.equals("Group chat") ? ui.t("Group chat") : name);
        }
        for (int i = 0; i < profiles.length(); i++) if (at(profiles, i).optString("id").equals(id)) return at(profiles, i);
        JSONObject remembered = inbox.remembered.get(id);
        return remembered == null ? person(id) : remembered;
    }

    private void detachPeople() {
        if (peopleMenu != null) peopleMenu.dismiss();
        peopleMenu = null;
        if (peoplePanel != null && peoplePanel.getParent() instanceof android.view.ViewGroup)
            ((android.view.ViewGroup) peoplePanel.getParent()).removeView(peoplePanel);
        peoplePanel = null;
        if (groupPicker != null) groupPicker.dismiss();
        groupPicker = null;
    }

    private void closePeople() {
        inbox.close();
        detachPeople();
    }

    private void renderPeople() {
        if (user == null) return;
        inbox.profiles(profiles);
        if (peopleMenu != null && peopleMenu.isShowing()) peopleMenu.bind(user.optString("id"), presence, inbox, this::peopleIdentity);
        if (peoplePanel != null) peoplePanel.bind(user.optString("id"), presence, workspaceReady && !presenceStale);
        if (groupPicker != null) {
            JSONObject group = inbox.groupInfo(groupPicker.target);
            JSONArray members = group == null ? new JSONArray() : array(group, "members");
            JSONArray available = new JSONArray();
            for (int i = 0; i < presence.length(); i++) {
                String id = at(presence, i).optString("id");
                boolean included = id.equals(user.optString("id"));
                for (int j = 0; j < members.length(); j++) included |= members.optString(j).equals(id);
                if (!included) available.put(peopleIdentity(id));
            }
            groupPicker.bind(available, group);
        }
    }

    private void openGroupPicker(String target) {
        if (groupPicker != null || user == null) return;
        groupPicker = new NativeGroupPicker(ui, inbox.groupInfo(target), (name, members, completion) -> {
            String request = UUID.randomUUID().toString();
            pendingGroups.put(request, completion);
            JSONObject payload = object("type", target.isEmpty() ? "group_create" : "group_invite", "request_id", request,
                    "name", name, "to", target, "members", new JSONArray(members), "visible", foreground);
            dispatchPeople(request, payload);
        }, group -> {
            if (groupPicker != null) groupPicker.dismiss();
            groupPicker = null;
            openPeople(group.optString("id"));
        });
        groupPicker.setOnDismissListener(dialog -> groupPicker = null);
        renderPeople();
        groupPicker.show();
    }

    private void sendPeople() {
        if (user == null || inbox.active.isEmpty()) return;
        if (!workspaceReady || presenceStale || workspaceStream == null) { inbox.errors.put(inbox.active, "unavailable"); renderPeople(); return; }
        String target = inbox.active;
        if (NativePeopleModel.group(target) ? !target.equals(NativePeopleModel.ROOM) && inbox.groupInfo(target) == null : NativePeopleModel.status(presence, target).isEmpty()) {
            inbox.errors.put(target, NativePeopleModel.group(target) ? "forbidden" : "offline"); renderPeople(); return;
        }
        String request = UUID.randomUUID().toString();
        NativePeopleModel.Send sending = inbox.begin(target, request);
        if (sending == null) return;
        dispatchPeople(request, object("type", NativePeopleModel.group(target) ? "group_message" : "direct_message", "request_id", request,
                "to", target, "text", sending.text, "visible", foreground));
        renderPeople();
    }

    private void dispatchPeople(String request, JSONObject payload) {
        EventStream captured = workspaceStream;
        int token = generation, session = authEpoch, streamEpoch = workspaceEpoch;
        if (!workspaceReady || presenceStale || captured == null) { failPeople(request, "unavailable"); return; }
        io.execute(() -> {
            if (token != generation || session != authEpoch || streamEpoch != workspaceEpoch) return;
            try { captured.send(payload.toString()); }
            catch (Exception error) { post(token, () -> {
                if (session != authEpoch || streamEpoch != workspaceEpoch) return;
                failPeople(request, "unknown");
                if (workspaceEvents != null) workspaceEvents.failed();
            }); }
        });
        main.postDelayed(() -> { if (token == generation && session == authEpoch && streamEpoch == workspaceEpoch) failPeople(request, "unknown"); }, 10000);
    }

    private void failPeople(String request, String code) {
        inbox.fail(request, code);
        NativeGroupPicker.Completion completion = pendingGroups.remove(request);
        if (completion != null) completion.done(null, code);
        renderPeople();
    }

    private void disconnectPeople() {
        inbox.disconnected();
        for (NativeGroupPicker.Completion completion : new ArrayList<>(pendingGroups.values())) completion.done(null, "unknown");
        pendingGroups.clear();
        renderPeople();
    }

    private void openFiles(String path) {
        closeDrawer();
        panel = "files";
        NativeDrawer overlay = new NativeDrawer(ui, true);
        drawer = overlay;
        if (fileState == null || !fileState.project.equals(projectID) || !fileState.chat.equals(chatID)) {
            fileState = new NativeFileModel(projectID, chatID);
            fileState.split = getPreferences(MODE_PRIVATE).getFloat("fileTreeFraction", .32f);
        }
        NativeFileModel ownerFiles = fileState;
        Client fileClient = client;
        fileBrowser = new NativeFileBrowser(ui, projectID.isEmpty() ? "" : projectName(), fileState, fileImages,
                (viewUi, apiPath) -> new NativePdfPreview(viewUi, imageWorker, address -> loadBinary(fileClient, address), apiPath),
                this::readProjectFile, file -> downloadFile(ownerFiles.filePath(file.optString("path"), "download"), file.optString("name")),
                this::openExternalLink, this::closeDrawer,
                value -> getPreferences(MODE_PRIVATE).edit().putFloat("fileTreeFraction", (float) value).apply());
        overlay.sheet.addView(fileBrowser, new LinearLayout.LayoutParams(-1, -1));
        root.addView(overlay, new FrameLayout.LayoutParams(-1, -1));
        overlay.show();
        fileBrowser.start();
    }

    private void readProjectFile(String path, NativeFileBrowser.Completion completion) {
        Client captured = client;
        int token = generation, session = authEpoch;
        String auth = csrf, loc = locale();
        io.execute(() -> {
            try {
                if (token != generation || session != authEpoch) return;
                JSONObject value = (JSONObject) request(captured, path.substring(4), null, auth, loc);
                post(token, () -> { if (session == authEpoch) completion.done(value, null); });
            } catch (Exception error) {
                post(token, () -> {
                    if (session != authEpoch) return;
                    if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
                    else completion.done(null, error);
                });
            }
        });
    }

    private void openFile(NativeFileTarget target) {
        if (projectID.isEmpty()) return;
        if (fileBrowser == null) openFiles(".");
        fileBrowser.select(target);
    }

    private void previewBinary(String path, String title) {
        if (!NativeFileData.allowedPath(path)) { toast(new IOException("Invalid image API path")); return; }
        NativeImagePreview preview = showImagePreview(title, () -> downloadFile(path, title));
        fileImages.remote(preview.content.image, path, previewEdge(), preview.completion());
    }

    private NativeFileData loadBinary(Client captured, String path) throws Exception {
        if (!NativeFileData.allowedPath(path)) throw new IOException("Invalid file API path");
        if (captured == null) throw new IOException("No file server configured");
        JSONObject response = new JSONObject(captured.request("GET", path, "", "", ""));
        if (response.optInt("status") != 200) throw new ApiFailure(response.optInt("status"), tr("File unavailable", "文件不可用"));
        return NativeFileData.response(response);
    }

    private int previewEdge() {
        return Math.min(2048, Math.max(getResources().getDisplayMetrics().widthPixels, getResources().getDisplayMetrics().heightPixels));
    }

    private NativeImagePreview showImagePreview(String name, Runnable download) {
        closeImagePreview();
        NativeImagePreview preview = new NativeImagePreview(ui, name, download);
        imagePreview = preview;
        preview.setOnDismissListener(dialog -> { if (imagePreview == preview) imagePreview = null; });
        preview.show();
        return preview;
    }

    private void previewLocalImage(Attachment file) {
        NativeImagePreview preview = showImagePreview(file.name, () -> chooseDownload(file.file, file.name, authEpoch));
        fileImages.local(preview.content.image, file.id, file.file, previewEdge(), preview.completion());
    }

    private void previewInlineImage(String source, String name) {
        final int session = authEpoch;
        NativeImagePreview preview = showImagePreview(name, () -> {
            if (downloading) return;
            downloading = true;
            int token = generation, request = ++downloadRequest;
            io.execute(() -> {
                try {
                    NativeFileData data = NativeFileData.inline(source);
                    post(token, () -> {
                        if (session != authEpoch || request != downloadRequest) return;
                        downloading = false; chooseDownload(data, name, session);
                    });
                } catch (Exception error) { post(token, () -> {
                    if (session != authEpoch || request != downloadRequest) return;
                    downloading = false; toast(error);
                }); }
            });
        });
        fileImages.inline(preview.content.image, source, previewEdge(), preview.completion());
    }

    private void closeImagePreview() {
        NativeImagePreview preview = imagePreview;
        imagePreview = null;
        if (preview != null) preview.dismiss();
    }

    private void downloadFile(String path, String name) {
        if (downloading) return;
        downloading = true;
        int token = generation, session = authEpoch, request = ++downloadRequest;
        Client captured = client;
        io.execute(() -> {
            try {
                if (token != generation) return;
                NativeFileData file = loadBinary(captured, path);
                post(token, () -> {
                    if (session != authEpoch || request != downloadRequest) return;
                    downloading = false;
                    chooseDownload(file, name, session);
                });
            } catch (Exception error) {
                post(token, () -> {
                    if (session != authEpoch || request != downloadRequest) return;
                    downloading = false;
                    if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
                    else toast(error);
                });
            }
        });
    }

    private void chooseDownload(NativeFileData file, String name, int session) {
        if (session != authEpoch || downloading || destroyed || !foreground) return;
        String filename = name.replaceAll("[\\\\/\\p{Cntrl}]", "_");
        if (filename.isEmpty()) filename = "attachment";
        pendingDownload = new PendingDownload(file, session);
        downloading = true;
        try {
            startActivityForResult(new Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
                    .setType(file.mediaType).putExtra(Intent.EXTRA_TITLE, filename), SAVE_FILE);
        } catch (ActivityNotFoundException error) {
            pendingDownload = null;
            downloading = false;
            toast(error);
        }
    }

    private void openMessageLink(String address) {
        if (destroyed || !foreground || user == null) return;
        NativeFileTarget file = NativeFileTarget.parse(address);
        if (file != null) {
            if (!projectID.isEmpty()) openFile(file);
        } else openExternalLink(address);
    }

    private void openExternalLink(String address) {
        if (destroyed || !foreground || user == null) return;
        // Web anchors hand off on the user's click, without an extra in-app modal.
        dispatchExternal(address);
    }

    private void openBrowser(String address) {
        if (!OriginPolicy.isExternalWebURL(address)) return;
        dispatchExternal(address);
    }

    private void dispatchExternal(String address) {
        if (destroyed || !foreground || !NativeExternalLink.allowed(address)) return;
        try {
            startActivity(
                    new Intent(Intent.ACTION_VIEW, Uri.parse(address).normalizeScheme())
                            .addCategory(Intent.CATEGORY_BROWSABLE));
        } catch (ActivityNotFoundException e) {
            toast(e);
        }
    }

    private void back() {
        if (desktopDialog != null) {
            closeDesktop();
            return;
        }
        if (ui.activePopover != null) {
            ui.dismissMenus();
            return;
        }
        if (appearanceDialog != null) {
            closeAppearance();
            return;
        }
        if (settingsOpen) {
            closeSettings();
            return;
        }
        if (peoplePanel != null) {
            closePeople();
            return;
        }
        if (drawer != null) {
            closeDrawer();
            return;
        }
        if (!chatID.isEmpty()) {
            newChat(projectID);
            return;
        }
        finish();
    }

    // API 33+ uses the OnBackInvokedDispatcher registered in onCreate.
    @SuppressLint("GestureBackNavigation")
    @Override
    public void onBackPressed() {
        back();
    }

    @Override
    public void onConfigurationChanged(Configuration config) {
        super.onConfigurationChanged(config);
        render();
    }

    @Override
    protected void onStart() {
        super.onStart();
        foreground = true;
        monitor.setActive(settingsOpen);
        recovery.setActive(true);
        if (client != null) publishInterfaces();
        main.post(connectionRetry);
        refreshServerConnections(true);
        if (user != null && !routeLoginRequired) {
            resumeWorkspace(true);
            if (workspaceData != null) bindWorkspaceData(false);
            startChat();
            updateHistoryLoading();
            if (!inbox.active.isEmpty() && peoplePanel == null && !settingsOpen) openPeople(inbox.active);
        }
    }

    @Override public void onWindowFocusChanged(boolean focused) {
        super.onWindowFocusChanged(focused);
        if (focused) resumeWorkspace();
    }

    @Override
    protected void onStop() {
        foreground = false;
        if (connectionRouter != null) connectionRouter.cancel();
        if (routeSelecting) { client = null; connected = false; connecting = false; busy = false; }
        routeSelecting = false;
        for (ServerRuntime runtime : serverRuntimes.values()) {
            stopServerFeed(runtime);
            if (runtime.selecting) runtime.candidate = null;
            runtime.router.cancel(); runtime.selecting = false;
        }
        main.removeCallbacks(serverRefresh);
        closeDesktop();
        if (fileBrowser != null) fileBrowser.deactivate();
        if (historyScroll != null) historyScroll.suspend();
        if (ui != null) ui.dismissMenus();
        monitor.setActive(false);
        recovery.setActive(false);
        main.removeCallbacks(connectionRetry);
        closeImagePreview();
        detachPeople();
        suspendStreams();
        super.onStop();
    }

    @Override
    protected void onDestroy() {
        if (appUpdates != null) appUpdates.close();
        closeDesktop();
        setDesktopAvailable(false);
        if (fileBrowser != null) fileBrowser.deactivate();
        fileBrowser = null;
        destroyed = true;
        if (historyScroll != null) historyScroll.dispose();
        if (transcriptViewport != null) transcriptViewport.dispose();
        ++generation;
        for (ServerRuntime runtime : serverRuntimes.values()) { stopServerFeed(runtime); runtime.router.stop(); }
        monitor.stop();
        recovery.stop();
        dismissSettingsDialog();
        if (serverDialog != null) serverDialog.dismiss();
        if (appearanceDialog != null) { appearanceDialog.dismiss(); appearanceDialog = null; }
        closeAvatarPicker();
        closeImagePreview();
        detachPeople();
        stopStreams();
        main.removeCallbacksAndMessages(null);
        if (networkCallback != null) connectivity.unregisterNetworkCallback(networkCallback);
        Client captured = client;
        final boolean routed = connectionRouter != null;
        client = null;
        connectionWorker.execute(
                () -> {
                    if (captured != null && !routed)
                        try {
                            captured.close();
                        } catch (Exception ignored) {
                        }
                });
        connectionWorker.shutdown();
        httpsWorker.shutdown();
        io.shutdown();
        workspaceWorker.shutdown();
        workspaceSocketWorker.shutdown();
        workspaceProbeWorker.shutdown();
        streamCloser.shutdown();
        payloadWorker.shutdown();
        diagnostics.shutdown();
        avatarImages.clear();
        fileImages.clear();
        pendingDownload = null;
        // Revoked image reads skip their work; let queued animation-surface cleanup finish.
        imageWorker.shutdown();
        syntax.close();
        codeWorker.shutdown();
        super.onDestroy();
    }
}
