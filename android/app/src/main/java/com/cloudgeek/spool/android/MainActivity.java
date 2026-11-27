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
import java.util.concurrent.atomic.AtomicReference;

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
    private RelayDiscovery relayDiscovery;
    private String pairingOwner = "";
    private NativeDevicePairingDialog devicePairingDialog;
    private static final int SCAN_PAIRING = 49371;
    private NativeUi ui;
    private FrameLayout root, drawer;
    private LinearLayout historyContent;
    private NativeKeyedViews transcriptViews;
    private NativeTranscriptViewport transcriptViewport;
    private NativeMarkwonHistory markwonHistory;
    private boolean useMarkwonHistory;
    private final NativeTurnPresentation.Cache turnPresentations = new NativeTurnPresentation.Cache();
    private final Map<String, NativeActivityList> virtualActivityRows = new HashMap<>();
    private final java.util.Set<String> activityRebinds = new java.util.HashSet<>();
    private NativeMarkwonHistory activityRenderOwner;
    private NativeComposerDock composerDock;
    private NativeSidebar sidebar;
    private NativeSidebar preparedSidebar;
    private boolean sidebarPreparationPending;
    private final Runnable sidebarPreparation = this::prepareSidebar;
    private NativeConnecting connectingView;
    private boolean sidebarPageDeferred;
    private NativeAttachmentCards fileBar;
    private NestedScrollView history;
    private EditText composer, serverInput, username, password;
    private boolean composerFocusPending;
    private NativeUi.IconButton send;
    private NativeUi.ChoiceButton modelButton, permissionButton;
    private NativeUi.Composer composerView;
    private NativeRemoteAgents remoteAgents;
    private NativeQueuedMessages queuedView;
    // A queue request answers only over the connection and sign-in it was made with. Once either
    // changes, its answer is dropped, so it must not go on counting as in flight.
    private boolean delivering, deliveringDraft, editingQueue;
    private int deliveryGeneration, deliveryAuth;
    private static final class Delivery {
        final String id = UUID.randomUUID().toString(), server, owner, chatID, turnID, message;
        final boolean steer;
        final long created = System.currentTimeMillis();
        final List<Attachment> files;
        String status = "sending";
        Delivery(String server, String owner, String chatID, String turnID, String message, List<Attachment> files, boolean steer) {
            this.server = server; this.owner = owner;
            this.chatID = chatID; this.turnID = turnID; this.message = message; this.files = files; this.steer = steer;
        }
    }
    private final List<Delivery> localDeliveries = new ArrayList<>();
    private NativeUi.Header workspaceHeader;
    private NativeChatMenu chatMenu;
    private String chatMenuTarget = "";
    private String chatActionPending = "";
    private NativeAgentModelMenu modelMenu;
    private NativePopover modelPopover;
    private NativePermissionMenu permissionMenu;
    private boolean permissionLoading, permissionFailed, permissionSaving;
    private int permissionSaveAuth, permissionSaveGeneration;
    private int permissionRequest;
    private NativeSettings settingsDialog, appearanceDialog;
    private NativeDesktopDialog desktopDialog;
    private boolean desktopAvailable;
    private NativeConnectionSettings connectionSettings;
    private NativeServerSettings serverSettings;
    private ServerProfiles.Server newServer;
    private LinearLayout serverDelete;
    private NativeAdbSettings adbSettings;
    private NativeDialog adbDialog;
    /** The selected server's share; other servers keep their own controller and notification. */
    private AdbShare adbShare;
    private final Map<String, AdbSession> adbSessions = new HashMap<>();
    /** "" when the selected server accepts a share, its reason when not, null while unknown. */
    private String adbAvailability;
    private boolean adbAvailabilityBusy;
    private static final int ADB_NOTIFICATION_PERMISSION = 13;
    private int adbPermissionPort, adbPermissionSession = -1;
    private final Runnable adbPoll = this::refreshAdbAvailability;
    private NativeFileBrowser fileBrowser;
    private NativeProjectUpload projectUpload;
    private NativeFileModel fileState;
    private Button loginSubmit;
    private TextView heading, subheading, connectionText, loginError, connectionHint;
    private JSONObject user, chat, connectionInfo, permissionCatalog;
    private JSONArray projects = new JSONArray(), chats = new JSONArray(), agents = new JSONArray();
    private JSONArray profiles = new JSONArray(),
            presence = new JSONArray(),
            groups = new JSONArray();
    private final Map<String, Draft> drafts = new HashMap<>();
    private final Map<String, Draft> editedQueueDrafts = new HashMap<>();
    private final Map<String, NativeActivityList.State> activityStates = new java.util.LinkedHashMap<>() {
        @Override protected boolean removeEldestEntry(Map.Entry<String, NativeActivityList.State> entry) { return size() > 200; }
    };
    private final NativePeopleModel inbox = new NativePeopleModel();
    private final Map<String, NativeGroupPicker.Completion> pendingGroups = new java.util.LinkedHashMap<>();
    private final NativeSidebarState sidebarState = new NativeSidebarState();
    private final NativeSessionNotifications sessionNotifications = new NativeSessionNotifications();
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
        boolean selecting, socketFailed;
        Object federationSignIn;
        long lastFederationAttempt;
        int sessionEpoch;
        final Map<ConnectionMode, String> states = new java.util.EnumMap<>(ConnectionMode.class);
        final Map<ConnectionPlan.Endpoint, ConnectionRecovery.Snapshot> health = new HashMap<>();

        ConnectionRouter.Candidate<Client> recordSample(ConnectionRouter.Candidate<Client> sample, boolean currentSession) {
            boolean sameClient = candidate != null && candidate.client == sample.client;
            if (!sameClient) socketFailed = false;
            // An inconclusive probe does not invalidate a verified session on the same transport.
            // A stale probe must not undo a newer login or expiry, either.
            if (currentSession && (sample.sessionChecked || !sameClient)) candidate = sample;
            else candidate = new ConnectionRouter.Candidate<>(sample.endpoint, sample.client, sample.health,
                    sameClient ? candidate.session : null, !currentSession && sameClient && candidate.sessionChecked);
            return candidate;
        }
    }
    private final Map<String, ServerRuntime> serverRuntimes = new HashMap<>();
    private final ServerConnections<Client> serverConnections = new ServerConnections<>();
    /** 当前已验证会话的互信目标。不保存到服务器配置。 */
    private JSONArray federationTargets;
    private int federationAttempt;
    private ServerRuntime federationRuntime;
    private FederationEnrollment federationEnrollment;
    private boolean federationRelayLoading;
    private String federationPendingTab = "";
    private String selectedFederationTab = "";
    private String restoringTrustedServer = "";
    private final Runnable serverRefresh = this::refreshServerConnections;
    private static final class ServerWorkspace {
        String userID = "", projectID = "", chatID = "", backend = "codex", model = "", effort = "", permission = "";
        JSONObject chat;
        JSONArray projects = new JSONArray(), chats = new JSONArray(), agents = new JSONArray(), profiles = new JSONArray();
        boolean agentsInitialized, cached;
        final NativeSidebarState sidebar = new NativeSidebarState();
        final NativeSessionNotifications notifications = new NativeSessionNotifications();
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
    private final NativeModelSelection.Memory modelSelectionMemory = new NativeModelSelection.Memory();
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
    private final AtomicReference<spoolnet.ProjectFiles> activeDownload = new AtomicReference<>();
    private Toast downloadToast;
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
        final String name, path;
        final Client client;
        final int session;
        PendingDownload(NativeFileData file, String name, String path, Client client, int session) {
            this.file = file; this.name = name; this.path = path; this.client = client; this.session = session;
        }
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
        AdbShareService.clearOrphanedNotifications(this);
        useMarkwonHistory = true;
        // Production always uses the recycler; debug builds retain a persisted comparison switch.
        if ((getApplicationInfo().flags & android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) != 0) {
            android.content.SharedPreferences rendering = getSharedPreferences("rendering-trial", MODE_PRIVATE);
            useMarkwonHistory = rendering.getBoolean("markwon", true);
            if (getIntent().hasExtra("spool_markwon")) {
                useMarkwonHistory = getIntent().getBooleanExtra("spool_markwon", true);
                rendering.edit().putBoolean("markwon", useMarkwonHistory).apply();
            }
        }
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
        if (serverProfiles.active() != null && serverProfiles.active().trusted()) {
            restoringTrustedServer = serverProfiles.active().id;
            String source = serverProfiles.active().federationSource;
            serverProfiles.selected = serverProfiles.entries.stream().filter(server -> server.id.equals(source)).map(server -> server.id)
                    .findFirst().orElseGet(() -> serverProfiles.entries.stream().filter(server -> !server.trusted()).map(server -> server.id).findFirst().orElse(""));
        }
        connectionPlan = serverProfiles.active() == null ? new ConnectionPlan("", "", ConnectionMode.DIRECT) : serverProfiles.active().plan;
        connectionDraft = connectionPlan;
        origin = "";
        if (!connectionPlan.ordered(null).isEmpty()) {
            ConnectionPlan.Endpoint first = connectionPlan.ordered(null).get(0);
            origin = first.origin; connectionMode = first.mode;
        }
        // Resolve the private, origin-scoped Go session before exposing the login form.
        restoringSession = !origin.isEmpty() && serverProfiles.active().enabled;
        connectivity = getSystemService(ConnectivityManager.class);
        relayDiscovery = new RelayDiscovery(this, this::relayDiscovered);
        networkCallback =
                new ConnectivityManager.NetworkCallback() {
                    public void onAvailable(Network network) {
                        if (relayDiscovery != null) relayDiscovery.networkChanged();
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
        boolean historyVisible = (markwonHistory != null ? markwonHistory.isShown() : history != null && history.isShown()) && !settingsOpen && peoplePanel == null;
        boolean interactive = foreground && focused && historyVisible && !destroyed;
        Integer velocity = performanceProbe.fling(args, interactive, selectionEpoch);
        if (velocity != null) { if (markwonHistory != null) markwonHistory.fling(0, velocity); else history.fling(velocity); }
        Integer position = performanceProbe.position(args, interactive, selectionEpoch);
        Boolean reuse = performanceProbe.reuse(args, interactive, selectionEpoch);
        boolean controlAccepted = position != null && markwonHistory != null && markwonHistory.probePosition(position);
        if (reuse != null && markwonHistory != null) { markwonHistory.reuseActivityRows = reuse; controlAccepted = true; }
        String title = performanceProbe.value(args, "open-title", foreground && focused && !destroyed, selectionEpoch);
        if (title != null && !title.isEmpty()) {
            String match = null; int matches = 0;
            for (int i = 0; i < chats.length(); i++) if (title.equals(at(chats, i).optString("title"))) { match = at(chats, i).optString("id"); matches++; }
            if (matches == 1 && match != null && !match.isEmpty()) { openChat(match); controlAccepted = true; }
        }
        String scenario = performanceProbe.value(args, "expand-tools", interactive, selectionEpoch);
        JSONObject scenarioResult = "standard".equals(scenario) ? probeExpandedTools() : null;
        if (scenarioResult != null) controlAccepted = true;
        if ("retry".equals(performanceProbe.value(args, "history-retry", interactive, selectionEpoch)) && historyLoader != null && historyLoader.failed()) {
            loadEarlier(); controlAccepted = true;
        }
        JSONObject visibleContent = null;
        if ("visible".equals(performanceProbe.value(args, "verify-content", interactive, selectionEpoch)) && markwonHistory != null) {
            visibleContent = markwonHistory.probeVisibleContent(performanceProbe.token); controlAccepted = true;
        }
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
        if (markwonHistory != null) { rows = markwonHistory.blocks(); mounted = markwonHistory.getChildCount(); }
        writer.println("SPOOL_PERF " + object("probeVersion", 2, "probeToken", performanceProbe.token, "selectionEpoch", selectionEpoch,
                "foreground", foreground, "focused", focused, "historyVisible", historyVisible, "flingAccepted", velocity != null, "controlAccepted", controlAccepted,
                "scenario", scenarioResult == null ? new JSONObject() : scenarioResult,
                "visibleContent", visibleContent == null ? new JSONObject() : visibleContent,
                "historyHasMore", chat != null && chat.optJSONObject("history") != null && chat.optJSONObject("history").optBoolean("has_more"),
                "historyFailed", historyLoader != null && historyLoader.failed(),
                "turns", array(chat, "turns").length(), "parts", array(chat == null ? null : chat.optJSONObject("transcript"), "parts").length(),
                "views", diagnosticViewCount(root), "virtualRows", rows, "mountedRows", mounted,
                "turnPresentationBuilds", turnPresentations.builds, "renderer", markwonHistory == null ? "native-views" : "markwon-recycler", "renderPending", markwonHistory == null ? 0 : markwonHistory.pending(),
                "viewport", markwonHistory != null ? markwonHistory.metrics() : transcriptViewport == null ? new JSONObject() : transcriptViewport.metrics(),
                "scrollAnchor", historyScroll == null ? new JSONObject() : historyScroll.metrics(),
                "historyHeight", markwonHistory != null ? markwonHistory.contentHeight() : historyContent == null ? 0 : historyContent.getHeight(),
                "scrollY", markwonHistory != null ? markwonHistory.contentOffset() : history == null ? 0 : history.getScrollY(),
                "realtime", object("epoch", workspaceEpoch, "connected", workspaceReady, "people", presence.length(),
                        "avatarViews", workspaceHeader == null || workspaceHeader.online.getChildCount() == 0 ? 0
                                : System.identityHashCode(workspaceHeader.online.getChildAt(0))),
                "javaUsedBytes", runtime.totalMemory() - runtime.freeMemory(), "javaMaxBytes", runtime.maxMemory(),
                "toolDetails", ui == null || ui.toolDetails == null ? new JSONObject() : ui.toolDetails.metrics(),
                "textOrigin", NativeTextView.diagnostics(),
                "nativeAllocatedBytes", android.os.Debug.getNativeHeapAllocatedSize()));
    }

    private JSONObject probeExpandedTools() {
        if (markwonHistory == null || chat == null || historyCursor(chat).more || markwonHistory.pending() != 0 || NativeChatState.hasRunningTurn(chat)) return null;
        Map<String, NativeTurnPresentation> turns = turnPresentations.update(authEpoch + "\n" + selectionEpoch + "\n" + chatID, chat, java.util.Collections.emptySet());
        String chosen = null; int largest = -1, index = 0, chosenIndex = -1;
        for (Map.Entry<String, NativeTurnPresentation> entry : turns.entrySet()) {
            NativeTurnPresentation value = entry.getValue(); NativeActivityList.State state = activityState(chat, value.turn);
            state.update(false, state.count); state.open = false;
            if (value.process.size() > largest) { largest = value.process.size(); chosen = entry.getKey(); chosenIndex = index; }
            index++;
        }
        if (chosen == null || largest == 0) return null;
        NativeActivityList.State state = activityState(chat, turns.get(chosen).turn); state.open = true;
        renderChat();
        NativeActivityList rows = virtualActivityRows.get(chosen);
        int group = rows.probeExpansion(); renderChat();
        int imagePosition = markwonHistory.positionOf(chosen + "/activity/group:" + group);
        int start = Math.min(markwonHistory.blocks() - 30, imagePosition + 30);
        if (start < 0) return null;
        markwonHistory.probePosition(start);
        return object("turnIndex", chosenIndex, "groups", state.count, "imageGroups", state.imageGroups.size(),
                "openDetails", state.items.size(), "anchorPosition", imagePosition, "startPosition", start);
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
        if (remoteAgents != null) remoteAgents.reset();
        composerFocusPending = false;
        if (composer != null) {
            composer.setFocusable(false);
            ((android.view.inputmethod.InputMethodManager) getSystemService(INPUT_METHOD_SERVICE))
                    .hideSoftInputFromWindow(composer.getWindowToken(), 0);
        }
        composer = null;
        pickerDraft = null;
        for (Draft value : drafts.values()) { value.text = ""; value.files.clear(); }
        drafts.clear();
    }

    private void restoreEditedQueueDraft() {
        Draft restored = editedQueueDrafts.remove(chatID);
        if (restored == null) return;
        Draft current = draft();
        current.text = restored.text + (restored.text.isEmpty() || current.text.isEmpty() ? "" : "\n\n") + current.text;
        current.files.addAll(restored.files);
        if (composer != null) {
            composer.setText(current.text); composer.setSelection(current.text.length());
            renderFiles(); composer.requestFocus();
        }
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
        closeChatMenu();
        clearSidebarPreparation();
        if (ui != null) ui.dismissMenus();
        if (remoteAgents != null) remoteAgents.suspend();
        closeAvatarPicker();
        closeImagePreview();
        detachPeople();
        if (sidebar != null) sidebar.deactivate();
        sidebar = null;
        if (fileBrowser != null) fileBrowser.deactivate();
        NativeProjectUpload retainedUpload = reopenFiles ? projectUpload : null;
        if (retainedUpload != null) projectUpload = null;
        else closeProjectUpload();
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
        if (markwonHistory != null) markwonHistory.dispose();
        markwonHistory = null; activityRenderOwner = null; virtualActivityRows.clear(); activityRebinds.clear(); turnPresentations.clear();
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
        else if ((routeLoginRequired || user == null) && serverProfiles.active() != null && serverProfiles.active().trusted()) connectingPage();
        else if (routeLoginRequired) loginPage();
        else if (user == null && restoringSession) connectingPage();
        else if (user == null) loginPage();
        else workspacePage();
        setContentView(root);
        root.requestApplyInsets();
        if (reopenSidebar) openSidebar(false);
        else if (reopenFiles && user != null) openFiles(".", true, retainedUpload);
        if (retainedUpload != null && retainedUpload != projectUpload) retainedUpload.close();
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
        if (settingsServer() == null) { addServer(); return; }
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
        if (newServer != null && connectionSettings != null) {
            ConnectionPlan plan = connectionSettings.plan();
            if ((!serverSettings.name.getText().toString().trim().isEmpty() || !plan.ordered(null).isEmpty() || !plan.autoSwitch || newServer.enabled)
                    && !persistNewServer(plan)) return;
        }
        if (settingsOpen && serverSettings != null) serverSettings.commitName();
        settingsOpen = false;
        newServer = null;
        pairingOwner = "";
        dismissSettingsDialog();
        serverSettings = null;
        serverDelete = null;
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
            page.addView(ui.button(ui.t("Add server"), true, this::addServer));
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
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        page.bind(connectionMessage, connecting || routeSelecting || sessionCheckBusy || runtime != null && runtime.federationSignIn != null);
        FrameLayout.LayoutParams menu = new FrameLayout.LayoutParams(dp(32), dp(32), Gravity.TOP | Gravity.START);
        menu.topMargin = (dp(50) - dp(1) - dp(32)) / 2; menu.setMarginStart(dp(16));
        page.addView(ui.sidebarButton(this::openSidebar), menu);
        root.addView(page, new FrameLayout.LayoutParams(-1, -1));
    }

    private void updateConnectingPage() {
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        if (connectingView != null) connectingView.bind(connectionMessage, connecting || routeSelecting || sessionCheckBusy || runtime != null && runtime.federationSignIn != null);
        updateConnectionSettings(); renderNavigation();
    }

    private void retrySession() {
        if (serverProfiles.active() != null && serverProfiles.active().trusted()) { loginFromSidebar(); return; }
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
                                    federationTargets = value.optJSONArray("federation_targets");
                                    recordActiveSession(captured, value);
                                    rememberServerName(serverProfiles.active(), value);
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

    private void relayDiscovered(String id, String addresses) {
        if (destroyed) return;
        for (ServerRuntime runtime : serverRuntimes.values()) {
            ConnectionRouter.Candidate<Client> current = runtime.candidate;
            if (current == null || current.endpoint.mode != ConnectionMode.RELAY) continue;
            try {
                if (!new RelayPairing(current.endpoint.origin).id.equals(id)) continue;
                connectionWorker.execute(() -> { try { current.client.setLANAddresses(addresses); } catch (Exception ignored) {} });
            } catch (IllegalArgumentException ignored) {}
        }
    }

    private void connect(String address) {
        ConnectionPlan plan = new ConnectionPlan(connectionMode == ConnectionMode.DIRECT ? address : connectionPlan.direct,
                connectionMode == ConnectionMode.TAILSCALE ? address : connectionPlan.tailscale,
                connectionMode == ConnectionMode.RELAY ? address : connectionPlan.relay, connectionPlan.selected, connectionPlan.autoSwitch);
        applyConnectionPlan(plan, null);
    }

    private ConnectionRouter<Client> routes() { connectionRouter = routes(serverProfiles.active()); return connectionRouter; }

    private ConnectionRouter<Client> routes(ServerProfiles.Server owner) {
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(owner.id, key -> new ServerRuntime());
        if (runtime.router == null) runtime.router = new ConnectionRouter<>(connectionWorker, task -> main.post(task), new ConnectionRouter.Backend<Client>() {
            public Client open(ConnectionPlan.Endpoint endpoint) throws Exception {
                String directory = owner.directory(getNoBackupFilesDir(), endpoint.mode);
                return serverConnections.open(directory + "\0" + endpoint.origin, new ServerConnections.Factory<Client>() {
                    public Client device() throws Exception {
                        Client candidate;
                        if (endpoint.mode == ConnectionMode.DIRECT) candidate = Spoolnet.newDirectClient(directory, endpoint.origin);
                        else if (endpoint.mode == ConnectionMode.RELAY) {
                            candidate = owner.federationRelay ? Spoolnet.newFederationRelayClient(directory, endpoint.origin)
                                    : Spoolnet.newRelayClient(directory, endpoint.origin);
                            if (relayDiscovery != null) {
                                String local = relayDiscovery.addresses(new RelayPairing(endpoint.origin).id);
                                if (!local.isEmpty()) candidate.setLANAddresses(local);
                            }
                        } else {
                            Spoolnet.updateInterfaces(NetworkSnapshot.read(connectivity));
                            candidate = Spoolnet.newClientForDevice(directory, endpoint.origin, Build.MODEL);
                        }
                        return candidate;
                    }
                    public Client session(Client device) throws Exception {
                        return device.newSessionClient(owner.sessionDirectory(getNoBackupFilesDir()), owner.plan.direct, owner.plan.tailscale, owner.plan.relay);
                    }
                    public void close(Client candidate) { try { candidate.close(); } catch (Exception ignored) {} }
                });
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
            public void close(Client candidate) { serverConnections.close(candidate); }
        });
        return runtime.router;
    }

    private void applyConnectionPlan(ConnectionPlan draft, ConnectionMode preferred) {
        if (serverProfiles.active() == null) return;
        ConnectionPlan validated;
        try {
            ConnectionMode selected = preferred == null ? draft.selected : preferred;
            for (ConnectionMode mode : ConnectionMode.values()) if (!draft.origin(mode).isEmpty()) {
                if (mode == ConnectionMode.RELAY && selected != ConnectionMode.RELAY) continue;
                try { if (mode == ConnectionMode.RELAY) new RelayPairing(draft.origin(mode)); else new OriginPolicy(draft.origin(mode)); }
                catch (Exception error) {
                    if (connectionSettings != null) connectionSettings.input(mode).setError(error.getMessage());
                    return;
                }
            }
            validated = (preferred == null ? draft : draft.select(preferred)).validated();
            if (validated.ordered(preferred).isEmpty()) throw new IllegalArgumentException(tr("Enter at least one HTTPS address.", "请至少填写一个 HTTPS 地址。"));
            if (preferred != null && validated.origin(preferred).isEmpty()) return;
        } catch (Exception error) { connectionMessage = error.getMessage(); updateConnectionSettings(); return; }
        if ((preferred == ConnectionMode.RELAY || preferred == null && validated.selected == ConnectionMode.RELAY)
                && !validated.relay.isEmpty() && new RelayPairing(validated.relay).invitation) {
            confirmDevicePairing(validated);
            return;
        }
        // A share may follow existing routes, but a newly configured endpoint has not been trusted for ADB.
        if (!sameConnectionAddresses(connectionPlan, validated)) {
            adbAvailability = null;
            stopAdbShare(); bindAdbSettings();
        }
        connectionPlan = connectionDraft = validated;
        shareConnectionPlan(serverProfiles.active(), validated);
        serverProfiles.active().enabled = true; saveServers();
        getPreferences(MODE_PRIVATE).edit().putString("directOrigin", validated.direct).putString("tailscaleOrigin", validated.tailscale)
                .putString("selectedNetwork", validated.selected.value).putBoolean("automaticConnections", validated.autoSwitch).apply();
        selectConnections(validated.selected, true);
    }

    private void confirmDevicePairing(ConnectionPlan plan) {
        if (devicePairingDialog != null && devicePairingDialog.isShowing()) return;
        ServerProfiles.Server owner = serverProfiles.active();
        if (owner == null) return;
        RelayPairing pairing = new RelayPairing(plan.relay);
        new NativePairingDialog(ui, pairing, () -> {
            if (destroyed || !owner.id.equals(serverProfiles.selected)) return;
            devicePairingDialog = new NativeDevicePairingDialog(ui, owner.directory(getNoBackupFilesDir(), ConnectionMode.RELAY), pairing, io, code -> {
                applyApprovedRelayPairing(owner, plan, code);
            });
            devicePairingDialog.show();
        }).show();
    }

    private void applyApprovedRelayPairing(ServerProfiles.Server owner, ConnectionPlan plan, String code) {
        if (destroyed || !owner.id.equals(serverProfiles.selected)) return;
        ConnectionPlan approved = new ConnectionPlan(plan.direct, plan.tailscale, code, ConnectionMode.RELAY, plan.autoSwitch);
        routes(owner).invalidate();
        routeSelecting = false;
        if (connectionSettings != null) connectionSettings.input(ConnectionMode.RELAY).setText(code);
        applyConnectionPlan(approved, ConnectionMode.RELAY);
        if (relayDiscovery != null) relayDiscovery.refresh();
    }

    private void selectConnections(ConnectionMode preferred, boolean manual) {
        if (routeSelecting || destroyed || !manual && (busy || pendingSend != null)) return;
        if (serverProfiles.active() == null || connectionPlan.ordered(preferred).isEmpty() || !serverProfiles.active().enabled) return;
        final ServerProfiles.Server owner = serverProfiles.active();
        if (owner.trusted() && !trustedProfileCurrent(owner)) return;
        final String serverID = owner.id;
        final ConnectionPlan selectedPlan = connectionPlan;
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(serverID, key -> new ServerRuntime());
        runtime.selecting = true;
        connectionWanted = true; routeSelecting = true;
        main.removeCallbacks(connectionRetry);
        final String account = user == null ? "" : user.optString("id");
        final int sessionEpoch = runtime.sessionEpoch;
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
                if (!serverProfiles.selected.equals(serverID)) { renderNavigation(); return; }
                routeStates.put(endpoint.mode, health.healthy() ? tr("Available", "可用")
                        : health.problem.equals("login") ? tr("Tailscale sign-in required", "需要登录 Tailscale")
                        : health.problem.equals("approval") ? tr("Device approval required", "等待设备批准") : tr("Unavailable", "不可用"));
                if (endpoint.mode == ConnectionMode.TAILSCALE && !health.authURL.isEmpty()) authURL = health.authURL;
                updateConnectionSettings();
            }
            public void selected(ConnectionRouter.Candidate<Client> sample) {
                runtime.selecting = false;
                if (destroyed) return;
                boolean currentSession = runtime.sessionEpoch == sessionEpoch;
                ConnectionRouter.Candidate<Client> candidate = runtime.recordSample(sample, currentSession);
                if (currentSession && candidate.session != null) try { rememberServerName(owner, new JSONObject(candidate.session.json)); }
                catch (Exception ignored) {}
                if (!serverProfiles.selected.equals(serverID)) {
                    if (currentSession && candidate.sessionChecked) {
                        if (candidate.session == null) forgetAccount(owner);
                        else try { rememberAccount(owner, new JSONObject(candidate.session.json).optJSONObject("user")); }
                        catch (Exception ignored) {}
                    }
                    syncServerFeed(owner); renderNavigation(); return;
                }
                routeSelecting = false; nextRouteProbe = android.os.SystemClock.elapsedRealtime() + 30000;
                adoptConnection(candidate, currentSession && candidate.sessionChecked);
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
        finishFederation();
        saveServerWorkspace(); closeDesktop(); stopStreams();
        // The draft was cleared when it was sent to the queue, and its answer will not arrive.
        final boolean unanswered = delivering() && deliveringDraft;
        for (Delivery delivery : localDeliveries) if (delivery.status.equals("sending")) delivery.status = "failed";
        ++generation; ++authEpoch; sessionCheckBusy = false;
        monitor.stop(); recovery.stop();
        client = null; connected = false; connecting = true;
        if (pendingSend != null || unanswered) {
            goalError = tr("Send status unknown. The message may have arrived; it will not be retried automatically.", "发送状态未知，消息可能已送达，不会自动重发。");
            pendingSend = null;
        }
        busy = true; updateComposer(); updateConnectionSettings(); renderNavigation();
    }

    private void adoptConnection(ConnectionRouter.Candidate<Client> candidate) {
        adoptConnection(candidate, candidate.sessionChecked);
    }

    private void adoptConnection(ConnectionRouter.Candidate<Client> candidate, boolean freshSessionCheck) {
        connectionWanted = true;
        ServerRuntime selectedRuntime = serverRuntimes.get(serverProfiles.selected);
        boolean trustedSigningIn = selectedRuntime != null && selectedRuntime.federationSignIn != null;
        if (client == candidate.client) {
            if (freshSessionCheck) {
                if (candidate.session == null && (user != null || hasWorkspaceCache())
                        && federationEnrollment == null && !trustedSigningIn) {
                    forgetAccount(); stopStreams(); clearWorkspace(); render();
                } else if (candidate.session != null && user != null && !routeLoginRequired && !restoringSession
                        && user.optString("id").equals(candidate.session.userID)) {
                    try { federationTargets = new JSONObject(candidate.session.json).optJSONArray("federation_targets"); }
                    catch (Exception ignored) {}
                }
            }
            acceptConnectionHealth(client, generation, candidate.health, candidate.health.healthy());
            updateConnectionSettings(); return;
        }
        saveServerWorkspace(); closeDesktop(); stopStreams();
        final boolean retained = user != null;
        for (Delivery delivery : localDeliveries) if (delivery.status.equals("sending")) delivery.status = "failed";
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
        federationTargets = null;
        if (candidate.sessionChecked && candidate.session == null) {
            if (!trustedSigningIn) forgetAccount();
            clearWorkspaceData(true);
        }
        if (retained && candidate.session != null && user.optString("id").equals(candidate.session.userID)) {
            try {
                JSONObject value = new JSONObject(candidate.session.json);
                user = value.optJSONObject("user"); csrf = value.optString("csrf");
                federationTargets = value.optJSONArray("federation_targets");
            } catch (Exception error) { routeLoginRequired = true; csrf = ""; }
            restoringSession = false;
        } else if (retained) {
            // 网络不可用不等于会话失效；等待 /me 核对后再恢复访问。
            routeLoginRequired = true; restoringSession = !candidate.sessionChecked; csrf = "";
        } else {
            routeLoginRequired = false; restoringSession = true;
            if (candidate.session != null) try {
                JSONObject value = new JSONObject(candidate.session.json);
                user = value.optJSONObject("user"); csrf = value.optString("csrf");
                federationTargets = value.optJSONArray("federation_targets");
                restoringSession = false;
                restoreServerWorkspace();
            } catch (Exception ignored) {}
        }
        final int token = generation;
        if (connectionMode != ConnectionMode.DIRECT) monitor.start(client::connectionInfo, snapshot -> {
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
        if (foreground && federationEnrollment != null && !federationEnrollment.settingsShown
                && federationEnrollment.endpoint.equals(new ConnectionPlan.Endpoint(connectionMode, origin))
                && (value.problem.equals("login") || value.problem.equals("approval"))) {
            federationEnrollment.settingsShown = true;
            openSettings();
            if (connectionSettings != null) connectionSettings.show(ConnectionMode.TAILSCALE);
        }
        probeAlternate(value);
        if (!value.healthy()) {
            if (definitiveConnectionFailure(value)) clearWorkspaceNavigation();
            return;
        }
        if (runtime != null && runtime.candidate != null && runtime.candidate.sessionChecked && runtime.candidate.session == null
                && !sessionCheckBusy && startTrustedSignIn(serverProfiles.active(), false)) return;
        if (restoringSession) restoreSession(captured, token);
        else if (routeLoginRequired) return;
        else if (recovered && user != null) {
            resumeWorkspace();
            if (chatEvents != null) chatEvents.stop();
            startChat();
        }
    }

    private void adoptRestoredSession(Client captured, JSONObject result, JSONObject account) {
        federationEnrollment = null;
        sessionCheckBusy = false;
        restoringSession = false; routeLoginRequired = false;
        user = account;
        csrf = result.optString("csrf");
        federationTargets = result.optJSONArray("federation_targets");
        rememberAccount(serverProfiles.active(), account);
        rememberServerName(serverProfiles.active(), result);
        recordActiveSession(captured, result);
        restoreServerWorkspace();
        render();
        refreshWorkspace();
        startWorkspace();
    }

    private static final class AutomaticFederationSource {
        final ServerProfiles.Server server;
        final ServerRuntime runtime;
        final Client client;
        final String account, csrf;
        AutomaticFederationSource(ServerProfiles.Server server, ServerRuntime runtime, Client client, String account, String csrf) {
            this.server = server; this.runtime = runtime; this.client = client; this.account = account; this.csrf = csrf;
        }

        boolean current() { return currentProfile() != null; }
        String accountKey() { return NativeFederation.accountKey(account, csrf); }

        JSONObject currentProfile() {
            ConnectionRouter.Candidate<Client> candidate = runtime.candidate;
            if (!server.enabled || candidate == null || candidate.client != client || candidate.session == null
                    || !candidate.session.userID.equals(account) || !server.plan.origin(candidate.endpoint.mode).equals(candidate.endpoint.origin)) return null;
            try {
                JSONObject profile = new JSONObject(candidate.session.json);
                return csrf.equals(profile.optString("csrf")) ? profile : null;
            } catch (Exception ignored) { return null; }
        }

        boolean offers(String target) {
            JSONObject profile = currentProfile();
            return profile != null && NativeFederation.parse(profile.opt("federation_targets")).stream().anyMatch(peer -> peer.id.equals(target));
        }
    }

    private AutomaticFederationSource federationSource(ServerProfiles.Server server) {
        if (server == null || !server.enabled || server.trusted()) return null;
        ServerRuntime runtime = serverRuntimes.get(server.id);
        ConnectionRouter.Candidate<Client> candidate = runtime == null ? null : runtime.candidate;
        if (candidate == null || candidate.session == null || !server.plan.origin(candidate.endpoint.mode).equals(candidate.endpoint.origin)) return null;
        try {
            JSONObject profile = new JSONObject(candidate.session.json);
            JSONObject account = profile.getJSONObject("user");
            String auth = profile.optString("csrf");
            if (!auth.isEmpty() && account.optString("source_server_id").isEmpty())
                return new AutomaticFederationSource(server, runtime, candidate.client, candidate.session.userID, auth);
        } catch (Exception ignored) { /* No verified local account. */ }
        return null;
    }

    private static final class FederationEnrollment {
        final AutomaticFederationSource source;
        final String target;
        final ConnectionPlan.Endpoint endpoint;
        final boolean requirePublishedMode;
        boolean settingsShown;
        FederationEnrollment(AutomaticFederationSource source, String target, ConnectionPlan.Endpoint endpoint, boolean requirePublishedMode) {
            this.source = source; this.target = target; this.endpoint = endpoint; this.requirePublishedMode = requirePublishedMode;
        }
    }

    private boolean federationAuthorized(AutomaticFederationSource source, String target, ConnectionMode mode,
                                         String address, String language, boolean requirePublishedMode) throws Exception {
        JSONObject profile = (JSONObject) decodeResponse(source.client.request("GET", "/api/me", "", "", language));
        JSONObject account = profile.getJSONObject("user");
        Object id = account.opt("id"), csrf = profile.opt("csrf"), from = account.opt("source_server_id");
        if (!(id instanceof String) || ((String) id).isEmpty() || !(csrf instanceof String) || ((String) csrf).isEmpty()
                || from != null && !(from instanceof String)) throw new IOException("Invalid session response");
        if (!source.account.equals(id) || !source.csrf.equals(csrf)) throw new IOException("Source session changed");
        if (from != null && !((String) from).isEmpty()) return false;
        JSONArray peers = (JSONArray) decodeResponse(source.client.request("GET", "/api/federation/targets", "", "", language));
        return NativeFederation.authorizedRoute(peers, target, mode, address, requirePublishedMode);
    }

    private boolean startAutomaticFederation(Client destination, int token) {
        if (token != generation || client != destination) return false;
        return startTrustedSignIn(serverProfiles.active(), federationEnrollment != null);
    }

    /** A confirmed 401 on an existing trusted profile can recover without selecting its tab. */
    private boolean startTrustedSignIn(ServerProfiles.Server server, boolean explicit) {
        if (!foreground || destroyed || server == null || !server.enabled || !server.trusted() || !trustedProfileCurrent(server)) return false;
        ServerRuntime runtime = serverRuntimes.get(server.id);
        ConnectionRouter.Candidate<Client> candidate = runtime == null ? null : runtime.candidate;
        if (candidate == null || !candidate.health.healthy() || !candidate.sessionChecked || candidate.session != null) return false;
        if (runtime.federationSignIn != null) return true;
        long now = android.os.SystemClock.elapsedRealtime();
        if (!explicit && runtime.lastFederationAttempt != 0 && now - runtime.lastFederationAttempt < 30000) return false;
        ServerProfiles.Server sourceServer = serverProfiles.entries.stream().filter(source -> source.id.equals(server.federationSource)).findFirst().orElse(null);
        AutomaticFederationSource owner = federationSource(sourceServer);
        if (owner == null) return false;
        final FederationEnrollment enrollment = server == serverProfiles.active() && federationEnrollment != null ? federationEnrollment
                : new FederationEnrollment(owner, server.federationTarget, candidate.endpoint, false);
        if (!enrollment.target.equals(server.federationTarget) || !enrollment.source.accountKey().equals(server.federationAccount)
                || !enrollment.endpoint.equals(candidate.endpoint) || !server.plan.origin(candidate.endpoint.mode).equals(candidate.endpoint.origin)) return false;
        final ConnectionPlan plan = server.plan;
        final int epoch = runtime.sessionEpoch;
        final Object attempt = new Object();
        final String language = locale();
        runtime.federationSignIn = attempt;
        runtime.lastFederationAttempt = Math.max(1, now);
        if (server == serverProfiles.active() && client == candidate.client) {
            federationEnrollment = null;
            sessionCheckBusy = true; restoringSession = true;
            sessionCheckGeneration = generation; sessionCheckAuth = authEpoch;
        }
        updateConnectingPage();
        io.execute(() -> {
            spoolnet.FederationSession result = null;
            try {
                if (federationAuthorized(enrollment.source, enrollment.target, candidate.endpoint.mode, candidate.endpoint.origin, language, enrollment.requirePublishedMode))
                    result = enrollment.source.client.prepareFederatedSession(candidate.client, enrollment.target, enrollment.source.account, language);
            } catch (Exception ignored) { /* Leave the trusted retry action available. */ }
            spoolnet.FederationSession exchange = result;
            main.post(() -> {
                if (runtime.federationSignIn != attempt) return;
                runtime.federationSignIn = null;
                boolean selected = server == serverProfiles.active() && client == candidate.client;
                boolean current = !destroyed && foreground && server.enabled && server.plan == plan && serverProfiles.entries.contains(server)
                        && serverRuntimes.get(server.id) == runtime && runtime.sessionEpoch == epoch && runtime.candidate != null
                        && runtime.candidate.client == candidate.client && runtime.candidate.endpoint.equals(candidate.endpoint) && runtime.candidate.session == null
                        && federationOwnerCurrent(enrollment.source, enrollment.target) && trustedProfileCurrent(server);
                if (selected) { sessionCheckBusy = false; restoringSession = false; }
                if (current && exchange != null) {
                    try {
                        JSONObject profile = new JSONObject(exchange.activate());
                        JSONObject account = profile.getJSONObject("user");
                        ++runtime.sessionEpoch;
                        runtime.candidate = new ConnectionRouter.Candidate<>(candidate.endpoint, candidate.client, runtime.candidate.health,
                                new ConnectionRouter.Session(account.getString("id"), profile.toString()));
                        rememberAccount(server, account);
                        rememberServerName(server, profile);
                        if (selected) adoptRestoredSession(candidate.client, profile, account);
                        else { syncServerFeed(server); renderNavigation(); }
                        return;
                    } catch (Exception ignored) { /* A changed session cannot be activated. */ }
                }
                if (destroyed) return;
                if (selected) { connectionMessage = ui.t("Could not sign in. Try again."); render(); }
                else renderNavigation();
            });
        });
        return true;
    }

    private void restoreSession(Client captured, int token) {
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        if (runtime != null && runtime.federationSignIn != null && runtime.candidate != null && runtime.candidate.client == captured) {
            sessionCheckBusy = true; restoringSession = true;
            updateConnectingPage(); return;
        }
        final int session = authEpoch;
        if (sessionCheckBusy && sessionCheckGeneration == token && sessionCheckAuth == session) return;
        sessionCheckBusy = true;
        sessionCheckGeneration = token; sessionCheckAuth = session;
        updateConnectingPage();
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
                                    adoptRestoredSession(captured, result, account);
                                });
                    } catch (Exception e) {
                        post(
                                token,
                                () -> {
                                    if (session != authEpoch) return;
                                    sessionCheckBusy = false;
                                    if (e instanceof ApiFailure && ((ApiFailure) e).status == 401) {
                                        // Invalidate the rejected session before any asynchronous federation work.
                                        forgetAccount();
                                        stopStreams();
                                        FederationEnrollment enrollment = federationEnrollment;
                                        clearWorkspace();
                                        federationEnrollment = enrollment;
                                        if (startAutomaticFederation(captured, token)) { render(); return; }
                                        render();
                                    } else {
                                        connectionMessage = e.getMessage();
                                        updateConnectingPage();
                                    }
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

    private void clearWorkspace(boolean forget) { clearWorkspace(forget, false); }

    private void clearWorkspace(boolean forget, boolean preserveAdb) {
        finishFederation();
        federationEnrollment = null;
        ++authEpoch;
        user = null;
        federationTargets = null;
        routeLoginRequired = false;
        restoringSession = false;
        sessionCheckBusy = false;
        csrf = "";
        clearWorkspaceData(forget, preserveAdb);
    }

    /** Detach the visible workspace without altering a freshly validated server login. */
    private void clearWorkspaceData(boolean forget) { clearWorkspaceData(forget, false); }

    private void clearWorkspaceData(boolean forget, boolean preserveAdb) {
        modelSelectionMemory.clear();
        closeChatMenu();
        chatActionPending = "";
        if (forget) serverWorkspaces.remove(serverProfiles.selected);
        workspaceUserID = ""; workspaceCached = false;
        if (sidebar != null && !sidebarPageDeferred) sidebar.deactivate();
        closeDesktop();
        setDesktopAvailable(false);
        closeAdbDialog();
        // A tab switch retains the server's share; clearing its login still stops it.
        adbAvailability = null;
        adbPermissionSession = -1;
        if (!preserveAdb) stopAdbShare();
        bindAdbSettings();
        if (workspaceData != null) workspaceData.stop();
        agentsInitialized = false;
        if (chatRefresh != null) chatRefresh.stop();
        if (fileBrowser != null) fileBrowser.deactivate();
        closeProjectUpload();
        fileBrowser = null;
        fileState = null;
        if (forget) avatarImages.clear();
        fileImages.clear();
        syntax.clear();
        closeImagePreview();
        pendingDownload = null;
        closeActiveDownload();
        if (downloadToast != null) downloadToast.cancel();
        downloading = false;
        downloadRequest++;
        ++selectionEpoch;
        chat = null;
        chatID = "";
        projectID = "";
        busy = false;
        pendingSend = null;
        optimisticSend = null;
        localDeliveries.clear();
        stoppingTurns.clear();
        goalNotice = goalError = "";
        discardComposerDrafts();
        editedQueueDrafts.clear();
        activityStates.clear();
        sidebarState.reset();
        sessionNotifications.clear();
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
        ServerProfiles.Server server = settingsServer();
        if (server == null) return;
        if (settingsDialog != null && connectionSettings != null && settingsDialog.matchesAppearance(settingsUi)
                && server.id.equals(settingsDialog.content.getTag())) {
            bindServerSettings(settingsUi, server);
            updateConnectionSettings();
            return;
        }
        boolean sameServer = settingsDialog != null && connectionSettings != null && server.id.equals(settingsDialog.content.getTag());
        ConnectionPlan plan = sameServer ? connectionSettings.plan() : newServer == server ? server.plan : connectionDraft;
        String name = sameServer ? serverSettings.name.getText().toString() : server.name;
        ConnectionMode visible = plan.selected;
        if (sameServer) for (ConnectionMode mode : ConnectionMode.values())
            if (connectionSettings.tabs.button(mode.value).isSelected()) visible = mode;
        if (serverSettings != null) serverSettings.name.setOnFocusChangeListener(null);
        if (connectionSettings != null) for (ConnectionMode mode : ConnectionMode.values())
            connectionSettings.input(mode).setOnFocusChangeListener(null);
        boolean show = settingsDialog == null;
        if (show) settingsDialog = new NativeSettings(settingsUi, theme, language, appVersion(),
                this::updatePreferences, this::closeSettings, false);
        else settingsDialog.bind(settingsUi, theme, language, body -> settingsAdditions(settingsUi, body, plan));
        if (show) {
            settingsAdditions(settingsUi, settingsDialog.content.additions, plan);
            settingsDialog.show();
        }
        settingsDialog.content.setTag(server.id);
        bindServerSettings(settingsUi, server);
        serverSettings.name.setText(name);
        connectionSettings.show(visible);
        updateConnectionSettings();
    }

    private ServerProfiles.Server settingsServer() { return newServer == null ? serverProfiles.active() : newServer; }

    private void bindServerSettings(NativeUi settingsUi, ServerProfiles.Server server) {
        boolean saved = serverProfiles.entries.contains(server);
        settingsDialog.content.title.setText(saved ? server.displayName() + " · " + settingsUi.t("Settings") : settingsUi.t("Add server"));
        serverSettings.bind(server);
        serverDelete.setVisibility(saved ? View.VISIBLE : View.GONE);
    }

    private void settingsAdditions(NativeUi settingsUi, LinearLayout body, ConnectionPlan initialPlan) {
        final ServerProfiles.Server server = settingsServer();
        serverSettings = new NativeServerSettings(settingsUi, server, enabled -> {
            if (server != settingsServer()) return;
            if (newServer == server) {
                if (!persistNewServer(connectionSettings.plan())) { serverSettings.bind(server); return; }
                newServer = null;
                connectionPlan = connectionDraft = server.plan;
                enableServer(server.id, enabled); settingsPage();
            } else enableServer(server.id, enabled);
        }, name -> {
            if (destroyed || !settingsOpen || server != settingsServer()) return;
            server.name = name;
            if (newServer == server) persistNewServer(server.plan);
            else { saveServers(); settingsPage(); renderNavigation(); }
        });
        body.addView(serverSettings);
        gap(body, 16);
        connectionSettings = new NativeConnectionSettings(settingsUi, initialPlan, plan -> {
            if (server != settingsServer()) return;
            if (newServer == server) {
                if (plan.autoSwitch != server.plan.autoSwitch) persistNewServer(server.plan.automatic(plan.autoSwitch));
                updateConnectionSettings();
            } else editConnectionPlan(plan);
        }, (plan, mode) -> {
            if (server != settingsServer()) return;
            if (newServer == server && !persistNewServer(plan.select(mode))) return;
            newServer = null;
            serverSettings.commitName();
            applyConnectionPlan(plan, mode);
        }, this::disconnect, () -> {
            if (server != settingsServer()) return;
            if (newServer == server && !persistNewServer(connectionSettings.plan().select(ConnectionMode.TAILSCALE))) return;
            newServer = null;
            serverSettings.commitName();
            enrollTailscale();
        }, this::scanPairing);
        if (server.federationRelay) connectionSettings.useFederationRelay();
        for (ConnectionMode mode : ConnectionMode.values()) connectionSettings.input(mode).setOnFocusChangeListener((view, focused) -> {
            if (!focused && settingsOpen && !destroyed && newServer == server && !connectionSettings.input(mode).getText().toString().trim().isEmpty())
                persistNewServer(connectionSettings.plan());
        });
        serverInput = connectionSettings.input(connectionSettings.plan().selected);
        connectionText = connectionSettings.diagnostics;
        connectionHint = connectionSettings.note;
        body.addView(connectionSettings);
        serverDelete = settingsUi.column();
        gap(serverDelete, 24);
        Button delete = settingsUi.button(settingsUi.t("Delete server"), false, this::deleteServer);
        delete.setTextColor(settingsUi.error);
        serverDelete.addView(delete, new LinearLayout.LayoutParams(-1, -2));
        body.addView(serverDelete);
    }

    private void scanPairing() {
        if (settingsServer() == null) return;
        pairingOwner = settingsServer().id;
        Intent scan = new Intent(this, PairingScanActivity.class).setAction(com.google.zxing.client.android.Intents.Scan.ACTION)
                .putExtra(com.google.zxing.client.android.Intents.Scan.FORMATS, "QR_CODE")
                .putExtra(com.google.zxing.client.android.Intents.Scan.PROMPT_MESSAGE, tr("Scan the pairing code in Spool Desktop", "扫描 Spool Desktop 显示的配对二维码"))
                .putExtra("ORIENTATION_LOCKED", false).putExtra("BEEP_ENABLED", false);
        startActivityForResult(scan, SCAN_PAIRING);
    }

    private void deleteServer() {
        ServerProfiles.Server removed = serverProfiles.active();
        if (removed == null) return;
        if (newServer == removed) newServer = null;
        closeSettings();
        List<ServerProfiles.Server> dependents = new ArrayList<>();
        for (ServerProfiles.Server server : serverProfiles.entries) {
            if (server == removed || !server.trusted()) continue;
            if (!server.federationSource.equals(removed.id) && !server.connectionID().equals(removed.id)) continue;
            dependents.add(server);
            removeAdbSession(server);
            ServerRuntime runtime = serverRuntimes.remove(server.id);
            if (runtime != null) { stopServerFeed(runtime); if (runtime.router != null) runtime.router.stop(); }
            serverWorkspaces.remove(server.id);
        }
        serverProfiles.entries.removeAll(dependents);
        removed.enabled = false;
        ServerRuntime previous = serverRuntimes.remove(removed.id);
        stopServerFeed(previous);
        ConnectionRouter<Client> previousRouter = previous == null ? connectionRouter : previous.router;
        if (previousRouter != null) previousRouter.stop();
        Client previousClient = client;
        stopStreams(); clearWorkspace(); serverWorkspaces.remove(removed.id);
        removeAdbSession(removed);
        ++generation; monitor.stop(); recovery.stop(); main.removeCallbacks(connectionRetry);
        connectionWanted = false; routeSelecting = routeLoginRequired = connected = connecting = false;
        backend = "codex"; model = effort = permission = "";
        client = null; connectionRouter = null; origin = authURL = connectionMessage = networkMessage = "";
        connectionInfo = null; httpsState = "Not checked"; tailnetState = "Disconnected";
        routeStates.clear(); nextRouteProbe = 0; routeFailures = 0;
        serverProfiles.remove(removed.id);
        selectAdbSession();
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
            if (removed.connectionID().equals(removed.id))
                for (ConnectionMode mode : ConnectionMode.values()) directories.add(removed.directory(getNoBackupFilesDir(), mode));
            directories.add(removed.sessionDirectory(getNoBackupFilesDir()));
            for (ServerProfiles.Server server : dependents) directories.add(server.sessionDirectory(getNoBackupFilesDir()));
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
        AdbSession sharing = adbSessions.get(server.id);
        if (sharing != null && !sharing.account.isEmpty() && !sharing.account.equals(user.optString("id"))) sharing.share.stop();
        JSONObject account = ServerProfiles.displayAccount(user);
        // 资料列表中的简版用户没有登录来源；不要因此清除当前账号的互信标记。
        if (account.optString("id").equals(server.account.optString("id"))
                && !user.has("username") && !server.account.optString("source_server_id").isEmpty()) {
            NativeChatState.put(account, "source_server_id", server.account.optString("source_server_id"));
        }
        ServerWorkspace cached = serverWorkspaces.get(server.id);
        if (cached != null && !cached.userID.equals(account.optString("id"))) serverWorkspaces.remove(server.id);
        if (server.account.toString().equals(account.toString())) return;
        server.account = account; saveServers();
    }

    private void rememberServerName(ServerProfiles.Server server, JSONObject value) {
        if (server == null) return;
        String name = ServerProfiles.serverName(value.optString("server_name"));
        if (server.serverName.equals(name)) return;
        server.serverName = name; saveServers();
        if (settingsOpen && settingsServer() == server) settingsPage();
        renderNavigation();
    }

    private void recordActiveSession(Client captured, JSONObject value) {
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        if (runtime == null) return;
        ++runtime.sessionEpoch;
        ConnectionRouter.Candidate<Client> previous = runtime.candidate;
        if (user != null && previous != null && previous.client == captured) {
            runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, captured, previous.health,
                    new ConnectionRouter.Session(user.optString("id"), value.toString()));
        }
    }

    private void rememberFederationTargets(ServerRuntime runtime, String account, JSONArray targets) throws Exception {
        ConnectionRouter.Candidate<Client> previous = runtime == null ? null : runtime.candidate;
        if (previous == null || previous.session == null || !previous.session.userID.equals(account)) return;
        JSONObject profile = new JSONObject(previous.session.json).put("federation_targets", targets);
        // 会话检查可能在 WebSocket 消息到达之前就已读取旧名单。
        ++runtime.sessionEpoch;
        runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, previous.client, previous.health,
                new ConnectionRouter.Session(account, profile.toString()), previous.sessionChecked);
    }

    private void forgetAccount() {
        workspaceUserID = ""; workspaceCached = false;
        forgetAccount(serverProfiles.active());
    }

    private void forgetAccount(ServerProfiles.Server server) {
        if (server == null) return;
        AdbSession sharing = adbSessions.get(server.id);
        if (sharing != null) sharing.share.stop();
        serverWorkspaces.remove(server.id);
        ServerRuntime runtime = serverRuntimes.get(server.id);
        stopServerFeed(runtime);
        if (runtime != null) {
            ++runtime.sessionEpoch;
            if (runtime.candidate != null) {
                ConnectionRouter.Candidate<Client> previous = runtime.candidate;
                runtime.candidate = new ConnectionRouter.Candidate<>(previous.endpoint, previous.client, previous.health, null, true);
            }
        }
        if (server.account.length() != 0) { server.account = new JSONObject(); saveServers(); }
        if (!server.id.equals(serverProfiles.selected)) { updateConnectionSettings(); renderNavigation(); }
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
        state.notifications.copyFrom(sessionNotifications);
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
                    new ConnectionRouter.Session(owner, object("user", user, "csrf", csrf, "federation_targets", federationTargets).toString()));
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
        sessionNotifications.copyFrom(state.notifications);
        profileCache.copyFrom(state.directory); historyScrollState.copyFrom(state.history);
        activityStates.putAll(state.activityStates); drafts.putAll(state.drafts);
        workspaceUserID = state.userID; workspaceCached = state.cached;
    }

    private boolean hasWorkspaceCache() {
        return workspaceCached && serverProfiles.active() != null
                && workspaceUserID.equals(serverProfiles.active().account.optString("id"));
    }

    private void selectServer(String id) { selectServer(id, null, ""); }

    private void selectServer(String id, FederationEnrollment enrollment, String federationTab) {
        restoringTrustedServer = "";
        if (serverProfiles.selected.equals(id)) return;
        ServerProfiles.Server next = null;
        for (ServerProfiles.Server server : serverProfiles.entries) if (server.id.equals(id)) next = server;
        if (next == null) return;
        ServerProfiles.Server previous = serverProfiles.active();
        rememberAccount();
        if (previous != null) {
            if (sameConnectionAddresses(previous.plan, connectionDraft)) previous.plan = connectionDraft;
            else shareConnectionPlan(previous, connectionDraft);
        }
        saveServerWorkspace(); closeDesktop();
        parkWorkspaceStream(previous);
        stopStreams();
        if (sidebar != null && panel.equals("sidebar")) deferWorkspacePage();
        clearWorkspace(false, true);
        backend = "codex"; model = effort = permission = "";
        ++generation; monitor.stop(); recovery.stop(); main.removeCallbacks(connectionRetry);
        routeSelecting = false; routeLoginRequired = false; connected = connecting = false;
        client = null; connectionRouter = null; origin = authURL = connectionMessage = networkMessage = "";
        connectionInfo = null; httpsState = "Not checked"; tailnetState = "Disconnected";
        serverProfiles.selected = next.id;
        selectAdbSession();
        selectedFederationTab = federationTab;
        federationEnrollment = enrollment;
        connectionPlan = connectionDraft = next.plan;
        routeStates.clear(); nextRouteProbe = 0; routeFailures = 0;
        ServerRuntime runtime = serverRuntimes.get(next.id);
        if (runtime != null) { connectionRouter = runtime.router; routeStates.putAll(runtime.states); }
        restoreServerWorkspace();
        saveServers(); panel = "sidebar";
        if (next.enabled && runtime != null && runtime.candidate != null) adoptConnection(runtime.candidate, runtime.candidate.sessionChecked);
        else {
            render();
            if (next.enabled && !connectionPlan.ordered(null).isEmpty()) selectConnections(null, true);
        }
        if (previous != null) syncServerFeed(previous);
    }

    private List<NativeFederation.Tab> federationTabs() {
        List<NativeFederation.Tab> tabs = new ArrayList<>();
        for (ServerProfiles.Server server : serverProfiles.entries) {
            AutomaticFederationSource source = federationSource(server);
            JSONObject profile = source == null ? null : source.currentProfile();
            if (profile != null) tabs.addAll(NativeFederation.tabs(serverProfiles, server,
                    NativeFederation.parse(profile.opt("federation_targets")), source.accountKey()));
        }
        return tabs;
    }

    private void chooseServer(String key) {
        if (key.equals(federationPendingTab)) return;
        NativeFederation.Tab tab = NativeFederation.find(federationTabs(), key);
        if (tab == null) { if (!key.startsWith(NativeFederation.PREFIX)) selectServer(key); return; }
        ServerRuntime savedRuntime = tab.server == null ? null : serverRuntimes.get(tab.server.id);
        if (savedRuntime != null && savedRuntime.federationSignIn != null
                || tab.server == serverProfiles.active() && sessionCheckBusy) return;
        AutomaticFederationSource owner = federationSource(tab.source);
        if (owner == null || !owner.offers(tab.target.id)) return;
        if (tab.target.clientMode == ConnectionMode.RELAY && (tab.route == null || tab.route.federationRelay)) {
            openFederationRelay(owner, tab.target, key);
            return;
        }
        if (tab.route != null) { openFederatedServer(owner, tab.target, tab.route, false); return; }
        if (federationRuntime != null) return;
        ConnectionPlan newPlan = tab.target.newPlan();
        if (newPlan == null) {
            Toast.makeText(this, tr("Add a Relay connection or ask the administrator for a trusted HTTPS address.",
                    "请添加 Relay 连接，或请管理员配置可信的 HTTPS 地址。"), Toast.LENGTH_LONG).show();
            return;
        }
        final int source = authEpoch, view = generation;
        NativeDialog confirm = new NativeDialog(ui, ui.t("Add server"), ui.t("Close"));
        confirm.body.addView(ui.text(tab.target.name + "\n" + tab.target.origin, 13, ui.text));
        confirm.actions.addView(ui.quietButton(ui.t("Cancel"), confirm::dismiss));
        confirm.actions.addView(ui.button(ui.t("Add server"), true, () -> {
            confirm.dismiss();
            NativeFederation.Tab latest = NativeFederation.find(federationTabs(), key);
            if (destroyed || !foreground || source != authEpoch || view != generation || federationRuntime != null
                    || !federationOwnerCurrent(owner, tab.target.id)
                    || latest == null || !latest.target.origin.equals(tab.target.origin) || latest.target.clientMode != tab.target.clientMode) return;
            if (latest.route != null) { openFederatedServer(owner, latest.target, latest.route, false); return; }
            ServerProfiles.Server server = new ServerProfiles.Server(UUID.randomUUID().toString(), latest.target.name, newPlan);
            server.plan = newPlan;
            openFederatedServer(owner, latest.target, server, true);
        }));
        confirm.show();
    }

    private void openFederationRelay(AutomaticFederationSource owner, NativeFederation.Target target, String key) {
        if (federationRelayLoading || federationRuntime != null) return;
        federationRelayLoading = true;
        federationPendingTab = key;
        renderNavigation();
        final int auth = authEpoch, view = generation;
        final String language = locale();
        io.execute(() -> {
            try {
                String code = owner.client.federationRelayRoute(target.id, owner.account, language);
                main.post(() -> {
                    federationRelayLoading = false;
                    federationPendingTab = "";
                    if (destroyed) return;
                    renderNavigation();
                    NativeFederation.Tab latest = NativeFederation.find(federationTabs(), key);
                    if (!foreground || auth != authEpoch || view != generation || !federationOwnerCurrent(owner, target.id)
                            || latest == null || latest.target.clientMode != ConnectionMode.RELAY) return;
                    ServerProfiles.Server route = new ServerProfiles.Server(UUID.randomUUID().toString(), "",
                            new ConnectionPlan("", "", code, ConnectionMode.RELAY, false));
                    route.serverName = latest.target.name;
                    route.federationRelay = true;
                    openFederatedServer(owner, latest.target, route, true);
                });
            } catch (Exception error) {
                main.post(() -> {
                    federationRelayLoading = false;
                    federationPendingTab = "";
                    if (destroyed) return;
                    renderNavigation();
                    if (foreground && auth == authEpoch && view == generation)
                        Toast.makeText(this, ui.t("Could not sign in. Try again."), Toast.LENGTH_LONG).show();
                });
            }
        });
    }

    private void openFederatedServer(AutomaticFederationSource owner, NativeFederation.Target target, ServerProfiles.Server route, boolean requirePublishedMode) {
        if (federationRuntime != null || !foreground || destroyed || !federationOwnerCurrent(owner, target.id)) return;
        ServerProfiles.Server previous = serverProfiles.entries.stream().filter(saved -> saved.federationSource.equals(owner.server.id)
                && saved.federationTarget.equals(target.id)).findFirst().orElse(null);
        if (previous != null && !previous.federationAccount.equals(owner.accountKey())) {
            ServerRuntime old = serverRuntimes.remove(previous.id);
            if (old != null) { stopServerFeed(old); if (old.router != null) old.router.stop(); }
            serverWorkspaces.remove(previous.id);
        }
        final ServerProfiles.Server server = serverProfiles.trustedProfile(route, owner.server, target.id, owner.accountKey());
        saveServers();
        final ConnectionPlan plan = server.plan;
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(server.id, key -> new ServerRuntime());
        if (runtime.federationSignIn != null || runtime.selecting) return;
        ConnectionRouter.Candidate<Client> current = runtime.candidate;
        ConnectionMode mode = NativeFederation.route(server, target, current == null ? plan.selected : current.endpoint.mode);
        if (mode == null) return;
        final ConnectionPlan.Endpoint endpoint = new ConnectionPlan.Endpoint(mode, plan.origin(mode));
        final FederationEnrollment enrollment = new FederationEnrollment(owner, target.id, endpoint, requirePublishedMode);
        final String tab = NativeFederation.tabKey(owner.server.id, target.id);
        if (server.enabled && current != null && current.session != null && current.health.healthy()
                && current.endpoint.equals(endpoint)) {
            selectServer(server.id, null, tab);
            return;
        }
        if (server == serverProfiles.active() && client != null) {
            selectedFederationTab = tab;
            renderNavigation();
            // 当前页已经在目标上；复用原生会话恢复流程，不重新接管正在使用的线路。
            if (user != null && !routeLoginRequired) return;
            if (!endpoint.equals(new ConnectionPlan.Endpoint(connectionMode, origin))) return;
            federationEnrollment = enrollment;
            restoreSession(client, generation);
            return;
        }
        final int auth = authEpoch, view = generation, targetSession = runtime.sessionEpoch;
        final String language = locale();
        federationRuntime = runtime;
        federationPendingTab = tab;
        final int attempt = ++federationAttempt;
        runtime.selecting = true;
        if (!server.enabled) { server.enabled = true; saveServers(); }
        Toast.makeText(this, ui.t("Signing in…"), Toast.LENGTH_SHORT).show();
        renderNavigation();
        // 先复核目标现有线路上的会话，即使该线路不允许收取互信票据。
        List<ConnectionPlan.Endpoint> endpoints = current != null && current.session != null && !endpoint.equals(current.endpoint)
                && plan.origin(current.endpoint.mode).equals(current.endpoint.origin) ? List.of(current.endpoint, endpoint) : List.of(endpoint);
        routes(server).select(endpoints, "", true, true, new ConnectionRouter.Listener<Client>() {
            public void replacing(Client previous) { stopServerFeed(runtime); runtime.candidate = null; }
            public void sampled(ConnectionPlan.Endpoint route, ConnectionRecovery.Snapshot health, boolean signedIn) {
                runtime.health.put(route, health);
                runtime.states.put(route.mode, health.healthy() ? tr("Available", "可用")
                        : health.problem.equals("login") ? tr("Tailscale sign-in required", "需要登录 Tailscale")
                        : health.problem.equals("approval") ? tr("Device approval required", "等待设备批准") : tr("Unavailable", "不可用"));
            }
            public void selected(ConnectionRouter.Candidate<Client> candidate) {
                if (federationAttempt != attempt) return;
                runtime.recordSample(candidate, runtime.sessionEpoch == targetSession);
                if (!federationViewCurrent(attempt, auth, view, server, plan) || !federationOwnerCurrent(owner, target.id)
                        || runtime.sessionEpoch != targetSession) {
                    finishFederationFailure(attempt, auth, view);
                    return;
                }
                if (candidate.session != null) {
                    finishFederation();
                    selectServer(server.id, null, tab);
                    return;
                }
                if (!candidate.endpoint.equals(endpoint)) {
                    finishFederationFailure(attempt, auth, view);
                    return;
                }
                if (!candidate.health.healthy()) {
                    if (mode == ConnectionMode.TAILSCALE && !candidate.health.problem.equals("endpoint")) {
                        finishFederation();
                        selectServer(server.id, enrollment, tab);
                    } else finishFederationFailure(attempt, auth, view);
                    return;
                }
                if (!candidate.sessionChecked) { finishFederationFailure(attempt, auth, view); return; }
                io.execute(() -> {
                    try {
                        if (generation != view || authEpoch != auth || destroyed) return;
                        if (!federationAuthorized(owner, target.id, mode, endpoint.origin, language, requirePublishedMode))
                            throw new IOException("Trusted server route changed");
                        if (generation != view || authEpoch != auth || destroyed) return;
                        spoolnet.FederationSession result = owner.client.prepareFederatedSession(candidate.client, target.id, owner.account, language);
                        main.post(() -> {
                            if (!federationViewCurrent(attempt, auth, view, server, plan) || !federationOwnerCurrent(owner, target.id)
                                    || runtime.sessionEpoch != targetSession || runtime.candidate != candidate) {
                                finishFederationFailure(attempt, auth, view);
                                return;
                            }
                            try {
                                JSONObject profile = new JSONObject(result.activate());
                                JSONObject person = profile.getJSONObject("user");
                                runtime.candidate = new ConnectionRouter.Candidate<>(candidate.endpoint, candidate.client, candidate.health,
                                        new ConnectionRouter.Session(person.getString("id"), profile.toString()));
                                rememberAccount(server, person);
                                rememberServerName(server, profile);
                                finishFederation();
                                selectServer(server.id, null, tab);
                            } catch (Exception error) { finishFederationFailure(attempt, auth, view); }
                        });
                    } catch (Exception error) { main.post(() -> finishFederationFailure(attempt, auth, view)); }
                });
            }
            public void unavailable() { finishFederationFailure(attempt, auth, view); }
        });
    }

    private boolean federationOwnerCurrent(AutomaticFederationSource owner, String target) {
        return serverProfiles.entries.contains(owner.server) && serverRuntimes.get(owner.server.id) == owner.runtime && owner.offers(target);
    }

    private boolean trustedProfileCurrent(ServerProfiles.Server server) {
        ServerProfiles.Server source = serverProfiles.entries.stream().filter(saved -> saved.id.equals(server.federationSource)).findFirst().orElse(null);
        AutomaticFederationSource owner = federationSource(source);
        return owner != null && owner.accountKey().equals(server.federationAccount) && owner.offers(server.federationTarget);
    }

    private boolean pruneTrustedSessions() {
        ServerProfiles.Server selected = serverProfiles.active();
        for (ServerProfiles.Server server : serverProfiles.entries) {
            if (!server.trusted() || trustedProfileCurrent(server)) continue;
            removeAdbSession(server);
            ServerRuntime runtime = serverRuntimes.remove(server.id);
            if (runtime != null) { stopServerFeed(runtime); if (runtime.router != null) runtime.router.stop(); }
            serverWorkspaces.remove(server.id);
        }
        if (selected == null || !selected.trusted() || trustedProfileCurrent(selected)) return false;
        String source = selected.federationSource;
        String next = serverProfiles.entries.stream().filter(server -> server.id.equals(source) && !server.trusted()).map(server -> server.id)
                .findFirst().orElseGet(() -> serverProfiles.entries.stream().filter(server -> !server.trusted()).map(server -> server.id).findFirst().orElse(""));
        if (next.isEmpty()) return false;
        // Do not persist a revoked workspace again while returning to its source.
        stopStreams(); clearWorkspace(false);
        selectServer(next);
        return true;
    }

    private boolean federationViewCurrent(int attempt, int auth, int view, ServerProfiles.Server server, ConnectionPlan plan) {
        return federationAttempt == attempt && federationRuntime != null && !destroyed && foreground
                && generation == view && authEpoch == auth && server.enabled && server.plan == plan && serverProfiles.entries.contains(server)
                && serverRuntimes.get(server.id) == federationRuntime;
    }

    private void finishFederation() {
        if (federationRuntime == null) return;
        federationRuntime.router.cancel();
        federationRuntime.selecting = false;
        federationRuntime = null;
        federationPendingTab = "";
        ++federationAttempt;
    }

    private void finishFederationFailure(int attempt, int auth, int view) {
        if (federationAttempt != attempt) return;
        finishFederation();
        if (destroyed || authEpoch != auth || generation != view) return;
        Toast.makeText(this, tr("Could not sign in to the trusted server. Try again.", "无法登录互信服务器，请重试。"), Toast.LENGTH_LONG).show();
        renderNavigation();
    }

    private void deferWorkspacePage() {
        sidebarPageDeferred = true;
        composer = null; composerView = null; composerDock = null;
        modelMenu = null; modelPopover = null; workspaceHeader = null;
        heading = subheading = null;
        if (transcriptViewport != null) transcriptViewport.dispose();
        transcriptViewport = null;
        if (markwonHistory != null) markwonHistory.dispose();
        markwonHistory = null; activityRenderOwner = null; virtualActivityRows.clear(); activityRebinds.clear(); turnPresentations.clear();
        if (historyScroll != null) historyScroll.dispose();
        historyScroll = null; history = null; historyContent = null; transcriptViews = null;
        connectingView = null; username = password = null; loginError = null; loginSubmit = null;
        ui.beforeDisclosure = view -> {};
        for (int i = root.getChildCount() - 1; i >= 0; i--) if (root.getChildAt(i) != drawer) root.removeViewAt(i);
    }

    private void enableServer(String id, boolean enabled) {
        for (ServerProfiles.Server server : serverProfiles.entries) if (server.id.equals(id)) {
            server.enabled = enabled; saveServers();
            if (!enabled) {
                AdbSession sharing = adbSessions.get(id);
                if (sharing != null) sharing.share.stop();
            }
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
        if (relayDiscovery != null && serverProfiles.entries.stream().anyMatch(s -> s.enabled && !s.plan.relay.isEmpty())) relayDiscovery.refresh();
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
                if (serverFeedCurrent(server, runtime, feed)) {
                    feed.cache.sidebar.bindPreferences(MainActivity.this, server.id, feed.userID);
                    acceptServerNavigation(feed.cache, value);
                }
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
                if (value) runtime.socketFailed = false;
                if (!value) feed.desktopAvailable = false;
                updateConnectionSettings(); renderNavigation();
            }
            public void expired() { if (serverFeedCurrent(server, runtime, feed)) forgetAccount(server); }
            public void authFailed(Exception error) {
                if (serverFeedCurrent(server, runtime, feed) && error instanceof ApiFailure && ((ApiFailure) error).status == 401)
                    forgetAccount(server);
            }
            public void failed() {
                if (!serverFeedCurrent(server, runtime, feed)) return;
                runtime.socketFailed = true;
                updateConnectionSettings(); renderNavigation();
                probeServer(server, true);
            }
            public void event(String kind, String raw) {
                if (!java.util.Arrays.asList("navigation", "agents", "profiles", "desktop", "presence", "server", "session_notification", "federation_targets").contains(kind)) return;
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
        else if (kind.equals("server") && value instanceof JSONObject) rememberServerName(server, (JSONObject) value);
        else if (kind.equals("federation_targets") && value instanceof JSONArray) {
            try {
                rememberFederationTargets(runtime, account, (JSONArray) value);
                renderNavigation();
            } catch (Exception ignored) { /* 保留上一次有效的会话快照。 */ }
        }
        else if (kind.equals("session_notification") && value instanceof JSONObject) feed.cache.notifications.receive((JSONObject) value, feed.userID, "");
    }

    private void acceptServerNavigation(ServerWorkspace cache, JSONObject value) {
        cache.sidebar.acceptPreferences(value.optJSONObject("sidebar"));
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
        if (destroyed || (!foreground && !sharingAdb(server.id)) || !server.enabled || server.plan.ordered(null).isEmpty()) return;
        if (server.trusted() && !trustedProfileCurrent(server)) return;
        ServerRuntime runtime = serverRuntimes.computeIfAbsent(server.id, key -> new ServerRuntime());
        if (runtime.selecting || runtime.federationSignIn != null) return;
        ConnectionRouter.Candidate<Client> current = runtime.candidate;
        if (startTrustedSignIn(server, false)) return;
        if (!force && current != null && current.health.healthy() && current.sessionChecked) {
            long age = current.client.responseAgeMillis();
            if (age >= 0 && age < ConnectionRecovery.QUIET_CHECK_MILLIS) return;
        }
        runtime.selecting = true;
        final int sessionEpoch = runtime.sessionEpoch;
        String account = current == null || current.session == null ? "" : current.session.userID;
        boolean awaitingLogin = current != null && (current.health.problem.equals("login") || current.health.problem.equals("approval")
                || current.health.problem.equals("starting") || current.health.healthy() && current.session == null);
        List<ConnectionPlan.Endpoint> endpoints = server.plan.autoSwitch && !awaitingLogin
                ? server.plan.ordered(current == null ? null : current.endpoint.mode)
                : server.plan.candidates(current == null ? server.plan.selected : current.endpoint.mode);
        // 所有可用线路都没有原会话时，也要接收明确的未登录结果，清理旧缓存。
        routes(server).select(endpoints, account, true, true, new ConnectionRouter.Listener<Client>() {
            public void replacing(Client previous) { stopServerFeed(runtime); runtime.candidate = null; }
            public void sampled(ConnectionPlan.Endpoint endpoint, ConnectionRecovery.Snapshot health, boolean signedIn) {
                runtime.health.keySet().retainAll(server.plan.ordered(null));
                runtime.health.put(endpoint, health);
                runtime.states.put(endpoint.mode, health.healthy() ? tr("Available", "可用") : tr("Unavailable", "不可用"));
                updateConnectionSettings(); renderNavigation();
            }
            public void selected(ConnectionRouter.Candidate<Client> sample) {
                runtime.selecting = false;
                if (!server.enabled || destroyed) return;
                boolean currentSession = runtime.sessionEpoch == sessionEpoch;
                boolean freshSessionCheck = currentSession && sample.sessionChecked;
                ConnectionRouter.Candidate<Client> candidate = runtime.recordSample(sample, currentSession);
                boolean selected = server.id.equals(serverProfiles.selected);
                if (freshSessionCheck && candidate.session != null) try {
                    JSONObject value = new JSONObject(candidate.session.json);
                    rememberServerName(server, value);
                    if (!selected) rememberAccount(server, value.optJSONObject("user"));
                } catch (Exception ignored) {}
                else if (freshSessionCheck && !selected) forgetAccount(server);
                if (freshSessionCheck && !selected && candidate.session == null && startTrustedSignIn(server, false)) return;
                if (selected) { routeStates.putAll(runtime.states); adoptConnection(candidate, freshSessionCheck); }
                else syncServerFeed(server);
                renderNavigation();
            }
            public void unavailable() { runtime.selecting = false; renderNavigation(); }
        });
    }

    private void addServer() {
        if (newServer != null) return;
        if (settingsOpen) closeSettings();
        newServer = new ServerProfiles.Server(UUID.randomUUID().toString(), "", new ConnectionPlan("", "", ConnectionMode.DIRECT, true));
        newServer.enabled = false;
        openSettings();
    }

    private boolean persistNewServer(ConnectionPlan plan) {
        if (newServer == null) return true;
        try {
            plan = plan.validated();
        } catch (IllegalArgumentException error) {
            if (connectionSettings != null) for (ConnectionMode mode : ConnectionMode.values()) {
                try {
                    String address = plan.origin(mode);
                    if (!address.isEmpty()) { if (mode == ConnectionMode.RELAY) new RelayPairing(address); else new OriginPolicy(address); }
                } catch (IllegalArgumentException invalid) { connectionSettings.input(mode).setError(invalid.getMessage()); }
            }
            return false;
        }
        ServerProfiles.Server server = newServer;
        server.name = serverSettings.name.getText().toString().trim();
        server.plan = plan;
        if (serverProfiles.entries.contains(server)) {
            connectionDraft = plan; saveServers(); settingsPage(); renderNavigation();
            return true;
        }
        // Keep this creation session's remaining fields until close or Connect.
        // The first profile has no previous server whose draft selectServer can capture.
        if (serverProfiles.entries.isEmpty()) connectionDraft = plan;
        serverProfiles.entries.add(server);
        selectServer(server.id);
        return true;
    }

    private void editConnectionPlan(ConnectionPlan plan) {
        connectionDraft = plan;
        if (plan.autoSwitch != connectionPlan.autoSwitch) {
            connectionPlan = connectionPlan.automatic(plan.autoSwitch);
            getPreferences(MODE_PRIVATE).edit().putBoolean("automaticConnections", plan.autoSwitch).apply();
            shareConnectionPlan(serverProfiles.active(), connectionPlan); saveServers();
            if (connectionRouter != null) connectionRouter.cancel();
            routeSelecting = false;
        }
        updateConnectionSettings();
    }

    private static boolean sameConnectionAddresses(ConnectionPlan first, ConnectionPlan second) {
        return first.direct.equals(second.direct) && first.tailscale.equals(second.tailscale) && first.relay.equals(second.relay);
    }

    private void shareConnectionPlan(ServerProfiles.Server owner, ConnectionPlan plan) {
        boolean changed = !sameConnectionAddresses(owner.plan, plan);
        owner.plan = plan;
        for (ServerProfiles.Server server : serverProfiles.entries) {
            if (changed && server.connectionID().equals(owner.connectionID())) removeAdbSession(server);
            if (server == owner || !server.connectionID().equals(owner.connectionID())) continue;
            server.plan = plan;
            ServerRuntime runtime = serverRuntimes.get(server.id);
            if (runtime != null) {
                stopServerFeed(runtime);
                if (runtime.router != null) runtime.router.stop();
                runtime.candidate = null; runtime.selecting = false; runtime.health.clear(); runtime.states.clear();
            }
        }
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
            public NativeDesktopFiles.Transport files(String token) throws Exception {
                return new NativeDesktopFileTransport(captured, token, auth, loc);
            }
            public NativeDesktopConnection.Wire open(String token, NativeDesktopConnection.Listener listener) throws Exception {
                spoolnet.DesktopStream stream = captured.openDesktop(token, new spoolnet.DesktopListener() {
                    public void onOpen() { listener.opened(); }
                    public void onMessage(byte[] data, boolean binary) { listener.message(data, binary); }
                    public void onClosed(long status) { listener.ended(); }
                });
                return new NativeDesktopConnection.Wire() {
                    public void send(String command) throws Exception { stream.send(command); }
                    public void image(byte[] png) throws Exception { stream.sendClipboardImage(png); }
                    public void close() { stream.close(); }
                };
            }
        }, io, () -> { if (desktopDialog != null && !desktopDialog.isShowing()) desktopDialog = null; }, serverProfiles.active().displayName());
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
        if (newServer != null) {
            ConnectionPlan plan = connectionSettings.plan();
            connectionSettings.bind(plan.selected, false, false, false, !plan.tailscale.isEmpty(), Map.of(), "");
            return;
        }
        String message = networkMessage == null || networkMessage.isEmpty() ? "" : "\n\n" + networkMessage;
        if (connectionMode == ConnectionMode.RELAY) {
            String path = connectionInfo == null ? "" : connectionInfo.optString("path");
            connectionHint.setText(tr("Paired identity verified on both LAN and relay.", "局域网和中继均验证已配对的服务器身份。"));
            if (connectionSettings != null) bindConnectionSettings((path.equals("lan") ? tr("LAN", "局域网直连") : path.equals("relay") ? tr("Relay", "中继") : tr("Connecting", "连接中")) + "\nHTTPS: " + httpsState + message);
            return;
        }
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
        NativeAttachmentDrop.Page page = ui.workspace();
        NativeUi.Header header = ui.new Header(tr("Workspace", "工作空间"), projectName(),
                this::openSidebar, this::openOnlinePeople, () -> openFiles("."));
        workspaceHeader = header;
        updateHeaderPeople();
        heading = header.title;
        subheading = header.subtitle;
        page.addView(header);
        page.addView(ui.divider());
        historyScrollState.select(authEpoch + "\n" + selectionEpoch + "\n" + chatID);
        if (useMarkwonHistory) {
            markwonHistory = new NativeMarkwonHistory(ui, historyScrollState, this::openMessageLink);
            ui.beforeDisclosure = markwonHistory::preservePosition;
        } else {
            // Delta-based fling steps preserve corrections when virtual rows change height.
            history = new NestedScrollView(this);
            history.setFillViewport(true);
            history.setVerticalScrollBarEnabled(false);
            historyContent = padded(12, 25);
            transcriptViews = new NativeKeyedViews(historyContent);
            history.addView(historyContent);
            historyScroll = new NativeHistoryScroll(history, historyContent, historyScrollState, 90 * getResources().getDisplayMetrics().density);
            ui.beforeDisclosure = historyScroll::preservePosition;
        }
        // Keep message/shadow overflow inside the scrolling viewport. The page
        // itself permits overflow so the composer's shadow can reach the history.
        FrameLayout historyViewport = new FrameLayout(this);
        historyViewport.addView(markwonHistory != null ? markwonHistory : history, new FrameLayout.LayoutParams(-1, -1));
        page.addView(historyViewport, new LinearLayout.LayoutParams(-1, 0, 1));
        composerView = ui.new Composer(composerHint(), this::pickFiles, this::chooseModel,
                this::choosePermission, this::sendOrStop);
        if (remoteAgents == null) remoteAgents = new NativeRemoteAgents(new NativeRemoteAgents.Host() {
            public void request(JSONObject input, java.util.function.Consumer<Object> done, java.util.function.Consumer<Exception> failed) {
                api("/federation/agents", input, done::accept, failed);
            }
            public void parent(java.util.function.Consumer<String> done, java.util.function.Consumer<Exception> failed) {
                ensureRemoteParent(done, failed);
            }
            public void changed() { updateComposer(); }
            public void error(Exception error) { commandFailed(error); }
            public void open(String url) { openBrowser(url); }
        });
        remoteAgents.attach(ui, composerView);
        composer = composerView.input;
        new NativeAttachmentDrop(this, ui, composerView.box,
                this::canAddAttachments, this::addAttachmentUris).advertiseOn(page);
        fileBar = composerView.attachments;
        modelButton = composerView.model;
        permissionButton = composerView.permission;
        send = composerView.send;
        queuedView = new NativeQueuedMessages(ui, id -> queueAction(id, "steer"), id -> queueAction(id, "delete"),
                this::editQueuedMessage, (id, before) -> queueAction(id, "move", before));
        composerView.addView(queuedView, composerView.indexOfChild(composerView.box), new LinearLayout.LayoutParams(-1, -2));
        composerView.commands.steer = () -> deliver(true);
        composerView.commands.queue = () -> deliver(false);
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
        final NativeMarkwonHistory recycler = markwonHistory;
        composerDock = new NativeComposerDock(ui, page, composerView, () -> {
            if (foreground && !destroyed && user != null) {
                if (recycler != null && markwonHistory == recycler) recycler.latest();
                else if (behavior != null && historyScroll == behavior) behavior.latest();
            }
        });
        composerDock.swipes(direction -> drawer == null && peoplePanel == null && !settingsOpen
                        && user != null && !routeLoginRequired && ui.activePopover == null,
                new NativeSwipeLayout.SwipeListener() {
                    private NativeDrawer opening;
                    @Override public void drag(int direction, float distance) {
                        if (opening == null) {
                            if (direction > 0) openSidebar(false); else openFiles(".", false);
                            opening = (NativeDrawer) drawer;
                        }
                        if (drawer == opening) opening.drag(distance, true);
                    }
                    @Override public void release(int direction, boolean commit) {
                        NativeDrawer previous = opening;
                        opening = null;
                        if (previous == null || drawer != previous) return;
                        if (commit) previous.show(); else closeDrawer();
                    }
                });
        if (recycler != null) recycler.onFollowing(composerDock::following); else behavior.onFollowing(composerDock::following);
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

    private boolean canUploadFiles() {
        if (user == null || routeLoginRequired) return false;
        for (int i = 0; i < projects.length(); i++)
            if (at(projects, i).optString("id").equals(projectID)) return at(projects, i).optBoolean("can_upload", false);
        return false;
    }

    private void closeProjectUpload() { if (projectUpload != null) { projectUpload.close(); projectUpload = null; } }

    private void updateComposer() {
        updateChatOptions();
        renderNavigation();
        if (composer == null || composerView == null) return;
        JSONObject activeTurn = NativeChatState.runningTurn(chat);
        boolean running = chat != null && chat.optString("status").equals("running");
        boolean stopping = activeTurn != null && (stoppingTurns.contains(activeTurn.optString("id"))
                || activeSubmission(activeTurn) != null && pendingSend.stopRequested);
        if (remoteAgents != null) remoteAgents.bind(authEpoch + "\n" + serverProfiles.selected + "\n" + projectID + "\n" + draftVersion,
                chatID, user != null && user.optString("source_server_id").isEmpty() && !routeLoginRequired,
                foreground && !destroyed, writable(), busy, running, federationTargets);
        boolean remote = remoteAgents != null && remoteAgents.targeted();
        boolean remoteBusy = remoteAgents != null && remoteAgents.busy();
        composerView.commands.catalog(backend, agentCommands(), commandCatalogMessage());
        composerView.commands.bind(writable(), busy || remoteBusy, running);
        composerView.commands.notice(goalNotice, () -> { goalNotice = ""; updateComposer(); });
        composerView.commands.error(goalError, () -> { goalError = ""; updateComposer(); });
        boolean editing = delivering() && editingQueue;
        boolean enabled = writable() && !editing && !remoteBusy;
        boolean focused = composer.hasFocus();
        composer.setEnabled(enabled);
        composer.setFocusable(enabled);
        composer.setFocusableInTouchMode(enabled);
        if (!enabled && focused)
            ((android.view.inputmethod.InputMethodManager) getSystemService(INPUT_METHOD_SERVICE))
                    .hideSoftInputFromWindow(composer.getWindowToken(), 0);
        focusComposerIfRequested();
        String hint = composerHint();
        if (!TextUtils.equals(composer.getHint(), hint)) composer.setHint(hint);
        composerView.add.setEnabled(canAddAttachments());
        modelButton.setEnabled(!busy && !remoteBusy && !running && writable());
        permissionButton.setEnabled(!busy && !remoteBusy && !remote && !running && writable());
        modelButton.setVisibility(projectID.isEmpty() ? View.GONE : View.VISIBLE);
        permissionButton.setVisibility(projectID.isEmpty() || remote ? View.GONE : View.VISIBLE);
        boolean hasDraft = !NativeGoalCommand.trim(composer.getText().toString()).isEmpty() || !draft().files.isEmpty();
        boolean showStop = activeTurn != null && !hasDraft;
        send.setKind(showStop ? stopping ? "spinner" : "stop" : "send");
        send.setContentDescription(showStop ? ui.t(stopping ? "Stopping…" : "Stop response") : ui.t("Send message"));
        send.setEnabled(writable() && (activeTurn != null ? hasDraft ? queueingDraft(activeTurn) && !delivering() : !stopping
                : !editing && !busy && selectedAgentAvailable() && permissionReady() && hasDraft));
        if (remote) {
            send.setKind(remoteBusy ? "spinner" : "send"); send.setContentDescription(ui.t("Send message"));
            send.setEnabled(!busy && !running && !remoteBusy && remoteAgents.ready() && hasDraft && draft().files.isEmpty());
        }
        if (queuedView != null) queuedView.bind(writable() && chat != null ? deliveryQueue() : null, activeTurn != null, delivering());
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
        if (remote) {
            NativeModelSelection remoteModel = remoteAgents.selection;
            modelButton.bind(remoteModel.model.isEmpty() ? ui.t("CLI default") : remoteModel.model,
                    remoteModel.backend.equals("claude") ? "claude" : "codex", ui.t("Choose agent and model"));
        }
    }

    private void focusComposerIfRequested() {
        if (!composerFocusPending || composer == null || !composer.isEnabled() || drawer != null
                || settingsOpen || routeLoginRequired || !foreground || destroyed) return;
        composerFocusPending = false;
        EditText target = composer;
        int selected = selectionEpoch, session = authEpoch;
        // Wait for the new page to attach; late snapshots must not focus an abandoned editor.
        main.post(() -> {
            if (composer != target || selected != selectionEpoch || session != authEpoch || !writable()
                    || !target.isEnabled() || !target.isAttachedToWindow() || drawer != null
                    || settingsOpen || routeLoginRequired || !foreground || destroyed) return;
            if (target.requestFocus())
                ((android.view.inputmethod.InputMethodManager) getSystemService(INPUT_METHOD_SERVICE))
                        .showSoftInput(target, android.view.inputmethod.InputMethodManager.SHOW_IMPLICIT);
        });
    }

    /** Save a parent record for direct delegation without starting a local Agent. */
    private void ensureRemoteParent(java.util.function.Consumer<String> done, java.util.function.Consumer<Exception> failed) {
        if (!chatID.isEmpty()) { done.accept(chatID); return; }
        final int selected = selectionEpoch;
        final String project = projectID;
        final Draft submitted = draft();
        api("/chats", object("project_id", project, "backend", backend), value -> {
            if (selected != selectionEpoch || !project.equals(projectID) || remoteAgents == null) return;
            JSONObject created = (JSONObject) value;
            String id = created.optString("id");
            if (id.isEmpty()) { failed.accept(new IOException(ui.t("The server returned an invalid response"))); return; }
            captureDraft(); remoteAgents.adoptChat(id); chatID = id; chat = created;
            drafts.remove("new/" + project); drafts.put(id, submitted); ++selectionEpoch;
            renderChat(); startChat(); done.accept(id);
        }, failed);
    }

    private void openSidebar() {
        openSidebar(true);
    }

    private void openSidebar(boolean animate) {
        closeDrawer(false);
        main.removeCallbacks(sidebarPreparation); sidebarPreparationPending = false;
        panel = "sidebar";
        NativeDrawer overlay = new NativeDrawer(ui, false, this::closeDrawer);
        drawer = overlay;
        sidebar = preparedSidebar == null ? createSidebar() : preparedSidebar;
        preparedSidebar = null;
        overlay.sheet.addView(sidebar, new LinearLayout.LayoutParams(-1, -1));
        root.addView(overlay, new FrameLayout.LayoutParams(-1, -1));
        renderNavigation();
        overlay.show(animate);
        refreshAdbAvailability();
    }

    private NativeSidebar createSidebar() {
        NativeSidebar next = new NativeSidebar(ui, sidebarState, this::person, this::closeDrawer, this::openChat, this::newChat,
                () -> { closeDrawer(); openAvatarPicker(); }, this::openSettings, this::logout, this::loginFromSidebar, this::openAppearance);
        next.chatOptions((value, anchor) -> openChatOptions(value, anchor, false));
        return next;
    }

    private void scheduleSidebarPreparation() {
        if (sidebarPreparationPending || destroyed || root == null || drawer != null || composerDock == null
                || user == null || routeLoginRequired || settingsOpen) return;
        sidebarPreparationPending = true;
        // Let the current page/closing animation finish before preparing the next entrance.
        main.postDelayed(sidebarPreparation, 220);
    }

    private void prepareSidebar() {
        sidebarPreparationPending = false;
        if (destroyed || root == null || drawer != null || composerDock == null || user == null
                || routeLoginRequired || settingsOpen) return;
        if (preparedSidebar == null) preparedSidebar = createSidebar();
        bindSidebar(preparedSidebar);
        int width = root.getWidth() - root.getPaddingLeft() - root.getPaddingRight();
        int height = root.getHeight() - root.getPaddingTop() - root.getPaddingBottom();
        if (width > 0 && height > 0) {
            preparedSidebar.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                    View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
            preparedSidebar.layout(0, 0, width, height);
        }
    }

    private void clearSidebarPreparation() {
        main.removeCallbacks(sidebarPreparation); sidebarPreparationPending = false;
        if (preparedSidebar != null) preparedSidebar.deactivate();
        preparedSidebar = null;
    }

    private void closeDrawer() {
        closeDrawer(true);
    }

    private void closeDrawer(boolean showPage) {
        if (fileBrowser != null) fileBrowser.deactivate();
        closeProjectUpload();
        fileBrowser = null;
        FrameLayout previous = drawer;
        drawer = null;
        if (sidebar != null) sidebar.deactivate();
        sidebar = null;
        panel = "";
        if (previous instanceof NativeDrawer) ((NativeDrawer) previous).dismiss();
        else if (previous != null) root.removeView(previous);
        scheduleSidebarPreparation();
        if (sidebarPageDeferred && showPage) {
            sidebarPageDeferred = false;
            readSessionNotification();
            render();
        }
        if (showPage) focusComposerIfRequested();
    }

    private Map<String, String> serverConnectionStates() {
        Map<String, String> states = new HashMap<>();
        for (ServerProfiles.Server server : serverProfiles.entries) {
            states.put(server.id, serverConnectionState(server));
            ServerRuntime runtime = serverRuntimes.get(server.id);
            if (server.trusted() && (runtime != null && runtime.federationSignIn != null || server == serverProfiles.active() && sessionCheckBusy))
                states.put(NativeFederation.tabKey(server.federationSource, server.federationTarget), "signing-in");
        }
        if (!federationPendingTab.isEmpty()) states.put(federationPendingTab, "signing-in");
        return states;
    }

    private String serverConnectionState(ServerProfiles.Server server) {
        if (!server.enabled || server.plan.ordered(null).isEmpty()) return "disconnected";
        ServerRuntime runtime = serverRuntimes.get(server.id);
        if (runtime != null && runtime.federationSignIn != null) return "connecting";
        ConnectionRouter.Candidate<Client> candidate = runtime == null ? null : runtime.candidate;
        boolean selected = server.id.equals(serverProfiles.selected);
        ConnectionPlan.Endpoint endpoint = selected && client != null ? new ConnectionPlan.Endpoint(connectionMode, origin)
                : candidate != null ? candidate.endpoint : server.plan.candidates(null).get(0);
        ConnectionRecovery.Snapshot health = runtime == null ? null : runtime.health.get(endpoint);
        if (health == null && candidate != null) health = candidate.health;
        if (health != null && !health.healthy())
            // Node enrollment/approval is transport setup, not Spool sign-in.
            return health.problem.equals("login") || health.problem.equals("approval") || health.problem.equals("starting") ? "connecting" : "error";
        if (health == null && !(selected && connected)) return "connecting";
        boolean ready = selected ? workspaceReady : runtime != null && runtime.feed != null && runtime.feed.events != null && runtime.feed.events.ready();
        if (selected) {
            if (user != null && !routeLoginRequired && !restoringSession)
                return ready ? "connected" : runtime != null && runtime.socketFailed ? "error" : "connecting";
            if (restoringSession || routeLoginRequired || sessionCheckBusy)
                return !sessionCheckBusy && candidate != null && !candidate.sessionChecked ? "error" : "connecting";
            return "login";
        }
        if (candidate == null) return "connecting";
        if (candidate.sessionChecked && candidate.session == null) return "login";
        if (ready) return "connected";
        if (candidate.session == null || runtime.socketFailed) return "error";
        return "connecting";
    }

    private void renderNavigation() {
        if (!restoringTrustedServer.isEmpty()) {
            ServerProfiles.Server saved = serverProfiles.entries.stream().filter(server -> server.id.equals(restoringTrustedServer)).findFirst().orElse(null);
            ServerProfiles.Server source = saved == null ? null : serverProfiles.entries.stream().filter(server -> server.id.equals(saved.federationSource)).findFirst().orElse(null);
            if (saved == null || !saved.enabled) restoringTrustedServer = "";
            else if (federationSource(source) != null) {
                restoringTrustedServer = "";
                if (trustedProfileCurrent(saved)) { selectServer(saved.id, null, NativeFederation.tabKey(source.id, saved.federationTarget)); return; }
            }
        }
        if (pruneTrustedSessions()) return;
        rememberAccount();
        restoreSidebarPreferences();
        sidebarState.select(projectID, chatID, projects, chats);
        if (!panel.equals("sidebar") || sidebar == null) { scheduleSidebarPreparation(); return; }
        bindSidebar(sidebar);
    }

    private void bindSidebar(NativeSidebar target) {
        target.servers(serverProfiles, serverConnectionStates(), this::chooseServer, this::addServer, federationTabs(), selectedFederationTab);
        boolean signedIn = user != null && !routeLoginRequired;
        boolean showWorkspace = signedIn || hasWorkspaceCache();
        boolean networkPending = routeSelecting || connecting && !httpsState.equals("Failed");
        ServerProfiles.Server selectedServer = serverProfiles.active();
        ServerRuntime selectedRuntime = selectedServer == null ? null : serverRuntimes.get(selectedServer.id);
        boolean trustedPending = selectedServer != null && selectedServer.trusted() &&
                (sessionCheckBusy || selectedRuntime != null && selectedRuntime.federationSignIn != null
                        || federationPendingTab.equals(NativeFederation.tabKey(selectedServer.federationSource, selectedServer.federationTarget)));
        boolean pending = networkPending || sessionCheckBusy || trustedPending;
        String action = trustedPending ? "Signing in…" : networkPending ? "Connecting…" : sessionCheckBusy ? "Restoring sign-in…" : !connected || client == null || restoringSession
                ? connectionWanted && serverProfiles.active() != null && serverProfiles.active().enabled ? "Retry" : "Connect"
                : signedIn ? "" : "Sign in";
        JSONObject account = signedIn ? ServerProfiles.displayAccount(person(user.optString("id")))
                : serverProfiles.active() == null ? new JSONObject() : serverProfiles.active().account;
        target.connection(account, action, pending);
        target.notifications(showWorkspace ? sessionNotifications : new NativeSessionNotifications());
        target.bind(signedIn ? person(user.optString("id")) : new JSONObject(), showWorkspace ? projects : new JSONArray(),
                showWorkspace ? chats : new JSONArray(), showWorkspace ? projectID : "", showWorkspace ? chatID : "",
                showWorkspace && chat != null && array(chat, "turns").length() == 0 ? backend : "", busy, !signedIn && showWorkspace);
        target.desktop(desktopAvailable, this::openDesktop);
        bindAdbSettings();
        if (target != sidebar) bindSidebarAdb(target);
    }

    private void loginFromSidebar() {
        if (serverProfiles.active() == null) { addServer(); return; }
        ServerProfiles.Server server = serverProfiles.active();
        ServerRuntime runtime = serverRuntimes.get(server.id);
        if (runtime != null && runtime.federationSignIn != null) return;
        if (routeSelecting || sessionCheckBusy) return;
        if (connectionPlan.ordered(null).isEmpty()) { openSettings(); return; }
        if (!serverProfiles.active().enabled) enableServer(serverProfiles.selected, true);
        else if (!connected || client == null) selectConnections(null, true);
        else if (server.trusted()) {
            if (!startTrustedSignIn(server, true)) restoreSession(client, generation);
        }
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
        if (!settingsOpen && (historyContent != null || markwonHistory != null)) renderChat();
    }

    private void acceptNavigation(JSONObject value) { acceptNavigation(value, ""); }

    private void acceptNavigation(JSONObject value, String removedChat) {
        restoreSidebarPreferences();
        sidebarState.acceptPreferences(value.optJSONObject("sidebar"));
        workspaceCached = true;
        workspaceUserID = user == null ? workspaceUserID : user.optString("id");
        projects = array(value, "projects");
        if (projectUpload != null) projectUpload.permissionsChanged();
        if (fileBrowser != null) fileBrowser.uploadPermissionsChanged();
        JSONArray incoming = array(value, "chats");
        chats = NativeNavigation.merge(chats, incoming);
        NativeNavigation.Selection selected = navigation.select(projectID, chatID, projects, incoming);
        // A command response also covers a newly created chat that has not appeared in SSE yet.
        if (!removedChat.isEmpty() && removedChat.equals(chatID) && !containsChat(incoming, removedChat))
            selected = new NativeNavigation.Selection(selected.project, "");
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
        composerFocusPending = true;
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

    private static boolean containsChat(JSONArray values, String id) {
        for (int i = 0; i < values.length(); i++) if (id.equals(values.optJSONObject(i).optString("id"))) return true;
        return false;
    }

    private String chatTitle() {
        String title = chat == null ? "" : chat.optString("title");
        return title.isEmpty() ? ui.t(projectID.isEmpty() ? "Workspace" : "New chat") : title;
    }

    private void updateChatOptions() {
        if (workspaceHeader == null) return;
        workspaceHeader.chatOptions(!chatID.isEmpty() && chat != null && user != null && !routeLoginRequired,
                chatActionPending.isEmpty() && connected, this::openChatOptions);
    }

    private void closeChatMenu() {
        if (chatMenu != null) chatMenu.dismiss();
        chatMenu = null;
        chatMenuTarget = "";
    }

    private void openChatOptions() {
        if (workspaceHeader == null || chat == null || user == null || routeLoginRequired || chatID.isEmpty() || !chatActionPending.isEmpty() || !connected) return;
        openChatOptions(chat, workspaceHeader.titleAction, true);
    }

    private void openChatOptions(JSONObject targetChat, View anchor, boolean selectedOnly) {
        if (user == null || routeLoginRequired || targetChat.optString("id").isEmpty() || !chatActionPending.isEmpty() || !connected) return;
        closeChatMenu();
        restoreSidebarPreferences();
        final String target = targetChat.optString("id");
        final int token = generation, session = authEpoch;
        chatMenuTarget = target;
        String title = targetChat.optString("title");
        chatMenu = new NativeChatMenu(ui, title.equals("New chat") ? ui.t("New chat") : title, new NativeChatMenu.Actions() {
            private JSONObject current() {
                if (destroyed || token != generation || session != authEpoch || user == null || routeLoginRequired || !connected) return null;
                if (target.equals(chatID)) return chat;
                if (!selectedOnly) for (int i = 0; i < chats.length(); i++)
                    if (target.equals(at(chats, i).optString("id"))) return at(chats, i);
                return null;
            }
            public boolean canManage() {
                JSONObject current = current();
                if (current == null || !current.optBoolean("can_control")) return false;
                if (target.equals(chatID)) return writable();
                for (int i = 0; i < projects.length(); i++)
                    if (at(projects, i).optString("id").equals(current.optString("project_id"))) return true;
                return false;
            }
            public boolean pinned() { return sidebarState.pinned.contains(target); }
            public void togglePin() {
                if (current() == null || !chatActionPending.isEmpty()) return;
                sidebarState.togglePin(target); renderNavigation();
            }
            public void rename(String title, java.util.function.Consumer<Exception> done) { mutate("rename", object("title", title), done); }
            public void archive(java.util.function.Consumer<Exception> done) { mutate("archive", object(), done); }
            public void delete(java.util.function.Consumer<Exception> done) { mutate("delete", object(), done); }
            private void mutate(String action, JSONObject body, java.util.function.Consumer<Exception> done) {
                if (!canManage() || !chatActionPending.isEmpty()) {
                    done.accept(new IOException(ui.t("Chat session changed. Try again.")));
                    return;
                }
                chatActionPending = target;
                updateChatOptions();
                api("/chats/" + Uri.encode(target) + "/" + action, body, value -> {
                    chatActionPending = "";
                    // Fence older fallback GETs before accepting the native mutation response.
                    if (workspaceData != null) workspaceData.navigationChanged();
                    if (action.equals("rename")) {
                        JSONObject metadata = (JSONObject) value;
                        if (chat != null && target.equals(chatID)) chat = NativeChatState.metadata(chat, metadata);
                        for (int i = 0; i < chats.length(); i++) if (target.equals(at(chats, i).optString("id")))
                            chats.put(i, NativeChatState.metadata(at(chats, i), metadata));
                        if (heading != null) heading.setText(chatTitle());
                        updateComposer();
                    } else {
                        acceptNavigation((JSONObject) value, target);
                    }
                    done.accept(null);
                }, error -> {
                    chatActionPending = "";
                    updateChatOptions();
                    done.accept(error);
                });
            }
        }, this::toast);
        chatMenu.show(anchor);
    }

    private void restoreSidebarPreferences() {
        ServerProfiles.Server server = serverProfiles.active();
        String owner = user != null ? user.optString("id") : server == null ? "" : server.account.optString("id");
        sidebarState.bindPreferences(this, serverProfiles.selected, owner);
        final NativeSidebarPreferences target = sidebarState.sync;
        if (target == null) return;
        final Client captured = client;
        final String auth = csrf, language = locale();
        final int token = generation, session = authEpoch;
        boolean writable = user != null && !routeLoginRequired && captured != null && connected;
        target.configure(writable ? (change, done, failed) -> io.execute(() -> {
            // Keep an already authorized operation bound to its original client
            // even when the user switches servers before the worker starts.
            try {
                JSONObject result = (JSONObject) request(captured, "/sidebar-preferences", change, auth, language);
                main.post(() -> { if (!destroyed) done.accept(result); });
            } catch (Exception error) {
                main.post(() -> { if (!destroyed) failed.accept(error); });
            }
        }) : null, () -> {
            if (!destroyed && sidebarState.sync == target) {
                boolean wasPinned = sidebarState.pinned.contains(chatMenuTarget);
                sidebarState.refreshPreferences();
                if (wasPinned != sidebarState.pinned.contains(chatMenuTarget) && chatMenu != null && chatMenu.popup.isShowing()) closeChatMenu();
                renderNavigation();
            }
        }, error -> {
            if (destroyed || token != generation || session != authEpoch || sidebarState.sync != target) return;
            if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
            else toast(error);
        });
    }

    private void openChat(String id) {
        sessionNotifications.dismiss(id);
        boolean deferred = sidebarPageDeferred;
        closeDrawer(false); sidebarPageDeferred = false;
        composerFocusPending = true;
        if (deferred && id.equals(chatID)) { render(); startChat(); return; }
        if (id.equals(chatID)) { focusComposerIfRequested(); return; }
        selectChat(id);
    }

    /** Restoring a selection from workspace data must preserve the user's open sidebar. */
    private void selectChat(String id) {
        sessionNotifications.dismiss(id);
        if (id.equals(chatID)) return;
        goalNotice = goalError = "";
        discardComposerDrafts();
        ++selectionEpoch;
        composerFocusPending = true;
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
        settleDeliveries();
        restoreEditedQueueDraft();
        java.util.Set<String> rebinds = pendingRebinds; pendingRebinds = null;
        updateChatRefresh();
        updateMissingProfiles();
        if (historyContent == null && markwonHistory == null) return;
        if (optimisticSend != null) {
            boolean visible = false;
            for (int i = 0; i < array(chat, "turns").length(); i++)
                visible |= optimisticSend.turnID.equals(at(array(chat, "turns"), i).optString("id"));
            if (!visible) optimisticSend = null;
        }
        Map<String, NativeTurnPresentation> turns = turnPresentations.update(authEpoch + "\n" + selectionEpoch + "\n" + chatID, chat, rebinds);
        if (markwonHistory != null) { renderMarkwonChat(turns, rebinds); return; }
        historyScroll.changing(anchor);
        transcriptViews.begin();
        updateHistoryLoading();
        NativeHistoryLoader.Cursor cursor = historyCursor(chat);
        boolean failed = historyLoader != null && historyLoader.failed();
        if (!cursor.error.isEmpty() || cursor.more)
            transcriptViews.use("history-notice", new JSONArray().put(!cursor.error.isEmpty()).put(cursor.more).put(failed),
                    () -> new NativeHistoryNotice(ui, !cursor.error.isEmpty(), cursor.more, failed, this::loadEarlier));
        if (chat == null || array(chat, "turns").length() == 0) {
            heading.setText(chatTitle());
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
        boolean virtual = array(chat, "turns").length() > 32 || array(chat.optJSONObject("transcript"), "parts").length() > 256 || transcriptViewport != null;
        if (virtual && transcriptViewport == null)
            transcriptViewport = new NativeTranscriptViewport(history, historyContent, historyScroll, historyScrollState);
        for (Map.Entry<String, NativeTurnPresentation> entry : turns.entrySet()) {
            String key = entry.getKey(); NativeTurnPresentation value = entry.getValue();
            String id = value.turn.optString("id");
            if (virtual) {
                JSONObject page = chat;
                NativeTranscriptViewport.Row row = (NativeTranscriptViewport.Row) transcriptViews.use(key + "/viewport", "viewport",
                        () -> new NativeTranscriptViewport.Row(ui));
                row.partCount = value.parts.length();
                if (rebinds == null || id.isEmpty() || rebinds.contains(id) || !row.bound()) row.bind(children -> renderTurn(page, value, key, children, true));
            } else renderTurn(chat, value, key, transcriptViews, true);
            renderLocalDeliveries(chat, value.turn, transcriptViews);
        }
        transcriptViews.end();
        historyScroll.changed();
        if (transcriptViewport != null) transcriptViewport.dataChanged();
        updateComposer();
    }

    private void renderMarkwonChat(Map<String, NativeTurnPresentation> turns, java.util.Set<String> rebinds) {
        NativeMarkwonHistory recycler = markwonHistory;
        updateHistoryLoading();
        recycler.begin();
        NativeHistoryLoader.Cursor cursor = historyCursor(chat);
        boolean failed = historyLoader != null && historyLoader.failed();
        if (!cursor.error.isEmpty() || cursor.more)
            recycler.chrome("history-notice", true, children -> children.use("notice", new JSONArray().put(!cursor.error.isEmpty()).put(cursor.more).put(failed),
                    () -> new NativeHistoryNotice(ui, !cursor.error.isEmpty(), cursor.more, failed, this::loadEarlier)));
        if (chat == null || array(chat, "turns").length() == 0) {
            heading.setText(chatTitle());
            subheading.setText(projectName());
            recycler.chrome("welcome", true, children -> children.use("welcome", projectID + "\n" + projectName(), () -> ui.welcome(
                    ui.t(projectID.isEmpty() ? "Prepare a project first." : "What shall we build today?"),
                    projectID.isEmpty() ? ui.t("Ask your desktop administrator to create a project and grant you access.")
                            : ui.t("Describe your idea. Your agent takes it from here."),
                    projectID.isEmpty() ? ui.t("Waiting for project access") : projectName(), ui.t("Run locally. Pick up anytime."))));
        } else {
            heading.setText(chat.optString("title", tr("New chat", "新对话"))); subheading.setText(projectName());
            JSONObject page = chat;
            java.util.Set<String> expanded = new java.util.HashSet<>();
            for (Map.Entry<String, NativeTurnPresentation> entry : turns.entrySet()) {
                String key = entry.getKey(); NativeTurnPresentation value = entry.getValue(); JSONObject turn = value.turn;
                String id = turn.optString("id");
                boolean refresh = rebinds == null || id.isEmpty() || rebinds.contains(id) || activityRebinds.contains(key);
                NativeActivityList.State state = activityState(page, turn);
                state.update(value.running && !value.answered, state.count);
                NativeActivityList rows = null;
                if (state.open && !value.process.isEmpty()) {
                    rows = virtualActivityRows.computeIfAbsent(key, ignored -> NativeActivityList.virtualRows(ui, state,
                            this::openMessageLink, transcriptImages(page), () -> requestActivityRender(recycler, key)));
                    rows.bindVirtual(value, turnUserInput(page, value)); expanded.add(key);
                }
                recycler.chrome(key + "/heading", refresh, children -> renderTurn(page, value, key, children, false));
                if (rows != null) rows.appendVirtualRows(recycler, key + "/activity", refresh);
                if (!value.tasks.isEmpty()) {
                    String taskKey = key + "/tasks";
                    NativeActivityList taskRows = virtualActivityRows.computeIfAbsent(taskKey, ignored -> NativeActivityList.virtualRows(ui,
                            new NativeActivityList.State(), this::openMessageLink, transcriptImages(page), () -> requestActivityRender(recycler, key)));
                    taskRows.bindVirtualTasks(value); expanded.add(taskKey);
                    taskRows.appendVirtualRows(recycler, taskKey, refresh);
                }
                if (!value.answer.isEmpty()) recycler.markdown(key + "/answer", value.answer);
                recycler.chrome(key + "/footer", refresh, children -> {
                    NativeTurnActions audit = (NativeTurnActions) children.use("actions", "actions", () -> new NativeTurnActions(ui));
                    audit.bind(value.actions);
                    if (!value.answer.isEmpty()) {
                        NativeMessageFooter footer = (NativeMessageFooter) children.use("footer", "footer",
                                () -> new NativeMessageFooter(ui, false, null, value.answer, turn.optLong("finished_at")));
                        footer.bindText(value.answer); footer.bindTimestamp(turn.optLong("finished_at"));
                    }
                    renderTurnStatus(page, value, key, children);
                    renderLocalDeliveries(page, turn, children);
                    children.use("gap", "", () -> ui.space(38));
                });
            }
            virtualActivityRows.keySet().retainAll(expanded);
        }
        activityRebinds.clear();
        recycler.end(); updateComposer();
    }

    private void requestActivityRender(NativeMarkwonHistory owner, String key) {
        if (destroyed || owner != markwonHistory) return;
        activityRebinds.add(key);
        if (activityRenderOwner == owner) return;
        activityRenderOwner = owner;
        main.post(() -> {
            if (activityRenderOwner != owner) return;
            activityRenderOwner = null;
            if (destroyed || owner != markwonHistory) return;
            pendingRebinds = java.util.Collections.emptySet(); renderChat();
        });
    }

    private NativeActivityList.State activityState(JSONObject page, JSONObject turn) {
        String key = NativeChatState.renderKey(turn);
        if (key.isEmpty()) key = page.optString("id") + ":" + turn.optInt("part_start");
        return activityStates.computeIfAbsent(key, ignored -> new NativeActivityList.State());
    }

    private JSONObject turnOperator(JSONObject page, JSONObject turn) {
        String id = turn.optString("user_id"); if (id.isEmpty()) id = page.optString("user_id");
        return id.isEmpty() ? null : person(id);
    }

    private NativeActivityList.UserInput turnUserInput(JSONObject page, NativeTurnPresentation value) {
        JSONObject operator = turnOperator(page, value.turn);
        return (row, parts) -> {
            JSONArray input = new JSONArray(parts);
            String text = NativeUserRow.text(input, 0, input.length());
            row.setVisibility(NativeUserRow.hasContent(text, new JSONArray(), input, 0, input.length()) ? View.VISIBLE : View.GONE);
            row.bind(operator, text, NativeUserRow.timestamp(input, 0, input.length(), value.turn.optLong("started_at")),
                    userFiles(page, value.turn, row, input, 0, input.length(), false));
            bindDeliveryStatus(row, input, 0, input.length());
        };
    }

    private void renderTurn(JSONObject page, NativeTurnPresentation value, String key, NativeKeyedViews transcriptViews, boolean renderAnswer) {
        JSONObject turn = value.turn, operator = turnOperator(page, turn);
        if (NativeUserRow.hasContent(value.input, value.attachments, value.parts, 0, value.inputEnd)) {
            NativeUserRow userRow = (NativeUserRow) transcriptViews.use(renderAnswer ? key + "/user" : "user", "user",
                    () -> new NativeUserRow(ui, operator, value.input, value.inputTimestamp, null, this::openMessageLink));
            userRow.bind(operator, value.input, value.inputTimestamp, userFiles(page, turn, userRow, value.parts, 0, value.inputEnd, true));
            bindDeliveryStatus(userRow, value.parts, 0, value.inputEnd);
            transcriptViews.use(renderAnswer ? key + "/user-gap" : "user-gap", "", () -> ui.space(22));
        }
        NativeActivityList.State activityState = activityState(page, turn);
        NativeTranscriptImages images = transcriptImages(page);
        NativeMarkwonHistory owner = markwonHistory;
        NativeTurn response = (NativeTurn) transcriptViews.use(renderAnswer ? key + "/response" : "response", "", () ->
                new NativeTurn(ui, value, activityState, this::openMessageLink, images, renderAnswer,
                        renderAnswer ? null : () -> requestActivityRender(owner, key)));
        response.rebindForTurn(activityState, images);
        response.activityRowsChanged(renderAnswer ? null : () -> requestActivityRender(owner, key));
        response.bind(value);
        response.userInput(turnUserInput(page, value));
        if (renderAnswer) {
            renderTurnStatus(page, value, key, transcriptViews);
            transcriptViews.use(key + "/turn-gap", "", () -> ui.space(38));
        }
    }

    private void renderTurnStatus(JSONObject page, NativeTurnPresentation value, String key, NativeKeyedViews children) {
        JSONObject approval = value.approval;
        if (approval != null) children.use(key + "/approval/" + approval.optString("id"),
                new JSONArray().put(approval).put(writable()).put(value.turn.optString("id")), () -> approvalCard(page.optString("id"), value.turn.optString("id"), approval));
        NativeTurnError.State error = activityState(page, value.turn).error;
        error.update(value.error, value.turn.optLong("finished_at"));
        if (!value.error.isEmpty()) children.use(key + "/error", new JSONArray().put(value.error).put(error.finishedAt), () -> new NativeTurnError(ui, error));
        if (value.running) children.use(key + "/working", "", () -> new NativeChatStatus(ui));
    }

    private List<NativeAttachmentCards.Item> userFiles(JSONObject page, JSONObject turn, NativeUserRow row, JSONArray parts, int start, int end, boolean first) {
        JSONArray files = first ? array(turn, "attachments") : new JSONArray();
        List<NativeAttachmentCards.Item> items = new ArrayList<>();
        final int session = authEpoch, selected = selectionEpoch;
        final String ownerChat = page.optString("id", chatID);
        java.util.function.BooleanSupplier current = () -> !destroyed && foreground && user != null && session == authEpoch
                && selected == selectionEpoch && ownerChat.equals(chatID) && row.isAttachedToWindow();
        if (first && optimisticSend != null && optimisticSend.turnID.equals(turn.optString("id")) && !optimisticSend.files.isEmpty()) {
            for (Attachment file : optimisticSend.files) {
                NativeImageResource resource = NativeImageResource.local(file.name, file.id, file.file);
                items.add(new NativeAttachmentCards.Item(file.id, file.name, file.file.mediaType, file.excerpt, "local:" + file.id,
                        () -> { if (current.getAsBoolean()) {
                            if (NativeFileData.image(file.file.mediaType)) previewLocalImage(file);
                            else chooseDownload(file.file, file.name, session);
                        } }, image -> resource.load(fileImages, image, Math.min(512, dp(120)), null)));
            }
        } else if (files.length() > 0) {
            for (int i = 0; i < files.length(); i++) {
                JSONObject file = at(files, i);
                if (ownerChat.isEmpty() || file.optString("id").isEmpty()) continue;
                String path = NativeFileData.attachmentPath(ownerChat, file.optString("id"));
                String name = file.optString("name"), media = file.optString("media_type");
                NativeImageResource resource = NativeImageResource.source(name, path);
                items.add(new NativeAttachmentCards.Item(file.optString("id"), name, media, file.optString("excerpt"), "remote:" + path,
                        () -> { if (current.getAsBoolean()) {
                            if (NativeFileData.image(media)) previewImage(resource); else downloadFile(path, name);
                        } }, image -> { if (resource != null) resource.load(fileImages, image, Math.min(512, dp(120)), null); }));
            }
        } else {
            for (int i = start; i < end; i++) {
                JSONObject part = at(parts, i);
                if (!part.optString("role").equals("user")) continue;
                String source = part.optString("image_url"), name = part.optString("name", "image");
                if ((part.optString("kind").equals("context") || part.optString("kind").equals("image") && !NativeFileData.inlineImage(source)) && !part.optString("name").isEmpty()) {
                    String filePath = part.optString("file_path");
                    String media = NativeFileData.imageType(name);
                    String imagePath = media.isEmpty() || filePath.isEmpty() ? "" : NativeFileData.filePath(page.optString("project_id", projectID), ownerChat, filePath, "image");
                    NativeImageResource resource = NativeImageResource.source(name, imagePath);
                    items.add(new NativeAttachmentCards.Item(ownerChat + ":" + turn.optString("id") + ":file:" + i, name, media.isEmpty() ? "application/octet-stream" : media, "", imagePath,
                            () -> { if (current.getAsBoolean()) { if (resource != null) previewImage(resource); else if (filePath.isEmpty()) toast(new IOException(tr("File unavailable", "文件不可用"))); else openFile(new NativeFileTarget(filePath, 0, 0)); } },
                            resource == null ? null : image -> resource.load(fileImages, image, Math.min(512, dp(120)), null)));
                    continue;
                }
                if (!part.optString("kind").equals("image") || !NativeFileData.inlineImage(source)) continue;
                String id = ownerChat + ":" + turn.optString("id") + ":image:" + i;
                NativeImageResource resource = NativeImageResource.source(name, source);
                items.add(new NativeAttachmentCards.Item(id, name, source.substring(5, source.indexOf(';')), "", source,
                        () -> { if (current.getAsBoolean()) previewImage(resource); },
                        image -> resource.load(fileImages, image, Math.min(512, dp(120)), null)));
            }
        }
        return items;
    }

    private NativeTranscriptImages transcriptImages(JSONObject page) {
        final int session = authEpoch, selected = selectionEpoch;
        final String ownerChat = page.optString("id", chatID), ownerProject = page.optString("project_id", projectID);
        return new NativeTranscriptImages() {
            private boolean ownsChat() { return !destroyed && user != null && session == authEpoch && selected == selectionEpoch && ownerChat.equals(chatID); }
            @Override public void load(android.widget.ImageView image, String path, String source, int edge, java.util.function.Consumer<Exception> completion) {
                if (!ownsChat()) { completion.accept(new IOException("Session changed")); return; }
                NativeImageResource resource = NativeImageResource.transcript(ownerProject, ownerChat, path, source, "image");
                if (resource == null) completion.accept(new NativeImageFailure(404));
                else resource.load(fileImages, image, edge, completion);
            }
            @Override public void preview(String path, String source, String name) {
                if (!foreground || !ownsChat()) return;
                previewImage(NativeImageResource.transcript(ownerProject, ownerChat, path, source, name));
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
        if (destroyed || user == null || client == null || chat == null || !chatID.equals(chat.optString("id")) || history == null && markwonHistory == null) {
            stopHistory(); return;
        }
        if (historyLoader == null) historyLoader = new NativeHistoryLoader<>(workspaceWorker, main::post);
        final Client captured = client;
        final String auth = csrf, loc = locale();
        String key = generation + "\n" + authEpoch + "\n" + selectionEpoch + "\n" + user.optString("id") + "\n" + chatID + "\n" + loc;
        final View surface = markwonHistory != null ? markwonHistory : history;
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
        if (remoteAgents != null && remoteAgents.targeted()) { remoteAgents.chooseModel(); return; }
        showModelMenu();
    }

    private void showModelMenu() {
        NativePopover menu = NativePopover.model(ui);
        NativeAgentModelMenu[] content = new NativeAgentModelMenu[1];
        content[0] = new NativeAgentModelMenu(ui, agents,
                new NativeModelSelection(backend, model, effort), chat == null ? "" : chat.optString("backend"), false, modelSelectionMemory, next -> {
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

    private JSONArray agentCommands() {
        JSONArray commands = permissionCatalog == null ? null : permissionCatalog.optJSONArray("commands");
        if (commands != null) return commands;
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = at(agents, i);
            if (agent.optString("backend").equals(backend)) {
                JSONObject catalog = agent.optJSONObject("catalog");
                return catalog == null ? null : catalog.optJSONArray("commands");
            }
        }
        return null;
    }

    private String commandCatalogMessage() {
        if (agentCommands() != null) return "";
        if (permissionLoading) return ui.t("Loading agent commands…");
        if (permissionFailed) return ui.t("Could not load agent commands. Check project permissions and retry.");
        if (permissionCatalog != null) return ui.t("This server does not provide an agent command catalog. Update Spool on the server.");
        return ui.t("Loading agent commands…");
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

    private String permissionPath() {
        return "/permissions?project_id=" + Uri.encode(projectID) + "&backend=" + Uri.encode(backend)
                + (chatID.isEmpty() ? "" : "&chat_id=" + Uri.encode(chatID));
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
                permissionPath(),
                null,
                value -> {
                    if (request != permissionRequest || selected != selectionEpoch || !selectedBackend.equals(backend)) return;
                    permissionCatalog = value instanceof JSONObject ? (JSONObject) value : new JSONObject();
                    permission = permissionCatalog.optString("current", permission);
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

    private boolean savingPermission() {
        return permissionSaving && permissionSaveAuth == authEpoch && permissionSaveGeneration == generation;
    }

    private boolean permissionReady() {
        if (permissionLoading || savingPermission()) return false;
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

    private void savePermission(String mode) {
        if (permissionLoading || savingPermission()) return;
        int selected = selectionEpoch, request = ++permissionRequest;
        String selectedBackend = backend, selectedProject = projectID;
        permissionLoading = permissionSaving = true;
        permissionSaveAuth = authEpoch; permissionSaveGeneration = generation;
        updatePermissionMenu();
        updateComposer();
        api(permissionPath(), object("mode", mode), value -> {
            permissionSaving = false;
            if (request != permissionRequest || selected != selectionEpoch || !selectedBackend.equals(backend)) {
                if (selectedProject.equals(projectID) && selectedBackend.equals(backend)) loadPermissions();
                else updateComposer();
                return;
            }
            permissionCatalog = (JSONObject) value;
            permission = permissionCatalog.optString("current");
            permissionLoading = permissionFailed = false;
            updatePermissionMenu();
            updateComposer();
        }, error -> {
            permissionSaving = false;
            if (request != permissionRequest || selected != selectionEpoch || !selectedBackend.equals(backend)) { updateComposer(); return; }
            permissionLoading = false;
            updatePermissionMenu();
            updateComposer();
            toast(error);
        });
    }

    private void choosePermission() {
        if (permissionMenu != null && permissionMenu.popover.isShowing()) { permissionMenu.popover.dismiss(); return; }
        if (permissionCatalog == null && !permissionLoading && !permissionFailed) loadPermissions();
        String selectedBackend = backend;
        int selected = selectionEpoch;
        permissionMenu = new NativePermissionMenu(ui, backend, permission, permissionCatalog, permissionLoading, permissionFailed, id -> {
            if (selected != selectionEpoch || !selectedBackend.equals(backend)) return;
            savePermission(id);
        }, this::loadPermissions);
        permissionMenu.show(permissionButton);
    }

    private boolean delivering() { return delivering && deliveryGeneration == generation && deliveryAuth == authEpoch; }

    private boolean deliveryPresent(Delivery delivery, boolean acceptedOnly) {
        if (chat == null || !ownsDelivery(delivery)) return false;
        JSONArray queue = array(chat, "queued_messages"), parts = array(chat.optJSONObject("transcript"), "parts");
        for (int i = 0; i < queue.length(); i++)
            if (delivery.id.equals(at(queue, i).optString("client_message_id"))) return true;
        for (int i = 0; i < parts.length(); i++) {
            JSONObject part = at(parts, i);
            if (part.optString("role").equals("user") && delivery.id.equals(part.optString("client_message_id"))
                    && (!acceptedOnly || !part.optBoolean("pending"))) return true;
        }
        return false;
    }

    private void settleDeliveries() {
        localDeliveries.removeIf(delivery -> deliveryPresent(delivery, true));
    }

    private boolean ownsDelivery(Delivery delivery) {
        return delivery.server.equals(serverProfiles.selected) && user != null && delivery.owner.equals(user.optString("id")) && delivery.chatID.equals(chatID);
    }

    private JSONArray deliveryQueue() {
        settleDeliveries();
        JSONArray queue = new JSONArray(), existing = array(chat, "queued_messages");
        for (int i = 0; i < existing.length(); i++) queue.put(existing.opt(i));
        for (Delivery delivery : localDeliveries) if (ownsDelivery(delivery) && !delivery.steer && !deliveryPresent(delivery, false)) {
            JSONArray names = new JSONArray(); for (Attachment file : delivery.files) names.put(file.name);
            queue.put(object("id", delivery.id, "message", delivery.message, "attachments", names,
                    "created_at", delivery.created, "delivery_status", delivery.status));
        }
        return queue;
    }

    private Delivery localDelivery(String id) {
        for (Delivery delivery : localDeliveries) if (ownsDelivery(delivery) && delivery.id.equals(id)) return delivery;
        return null;
    }

    private void editDelivery(Delivery delivery) {
        if (!writable() || !ownsDelivery(delivery) || !delivery.status.equals("failed")) return;
        captureDraft(); Draft current = draft();
        List<Attachment> combined = new ArrayList<>(current.files); combined.addAll(delivery.files);
        long bytes = 0; for (Attachment file : combined) bytes += file.data.length;
        if (combined.size() > 6 || bytes > 16 * 1024 * 1024) { goalError = ui.t("Attach at most 6 files, up to 16 MB total"); updateComposer(); return; }
        current.text = delivery.message + (current.text.isEmpty() ? "" : "\n\n" + current.text);
        current.files.clear(); current.files.addAll(combined); localDeliveries.remove(delivery);
        composer.setText(current.text); composer.setSelection(composer.length()); renderFiles(); renderChat(); composer.requestFocus();
    }

    private void bindDeliveryStatus(NativeUserRow row, JSONArray parts, int start, int end) {
        for (int i = start; i < end; i++) {
            Delivery delivery = localDelivery(at(parts, i).optString("client_message_id"));
            if (delivery != null) { row.delivery(delivery.status, () -> editDelivery(delivery)); return; }
        }
        row.delivery("", null);
    }

    private void renderLocalDeliveries(JSONObject page, JSONObject turn, NativeKeyedViews children) {
        JSONArray turns = array(page, "turns");
        for (Delivery delivery : localDeliveries) {
            if (!delivery.steer || !ownsDelivery(delivery) || deliveryPresent(delivery, false)) continue;
            boolean found = false;
            for (int i = 0; i < turns.length(); i++) found |= delivery.turnID.equals(at(turns, i).optString("id"));
            if (!delivery.turnID.equals(turn.optString("id")) && (found || turn != at(turns, turns.length() - 1))) continue;
            NativeUserRow row = (NativeUserRow) children.use("delivery/" + delivery.id, "user", () ->
                    new NativeUserRow(ui, user, delivery.message, delivery.created, null, this::openMessageLink));
            List<NativeAttachmentCards.Item> files = new ArrayList<>();
            final int session = authEpoch, selected = selectionEpoch;
            for (Attachment file : delivery.files) files.add(new NativeAttachmentCards.Item(file.id, file.name, file.file.mediaType,
                    file.excerpt, "local:" + file.id, () -> {
                        if (session != authEpoch || selected != selectionEpoch || !delivery.chatID.equals(chatID)) return;
                        if (NativeFileData.image(file.file.mediaType)) previewLocalImage(file); else chooseDownload(file.file, file.name, session);
                    }, image -> fileImages.local(image, file.id, file.file, Math.min(512, dp(120)), null)));
            row.bind(user, delivery.message, delivery.created, files);
            row.delivery(delivery.status, () -> editDelivery(delivery));
        }
    }

    private void beginDelivery(boolean draft) {
        editingQueue = false;
        delivering = true; deliveringDraft = draft; deliveryGeneration = generation; deliveryAuth = authEpoch;
    }

    private boolean queueingDraft(JSONObject activeTurn) {
        return activeTurn != null && pendingSend == null && composer != null
                && (!NativeGoalCommand.trim(composer.getText().toString()).isEmpty() || !draft().files.isEmpty());
    }

    /** Queue the draft behind the running turn, or steer it into that turn. */
    private void deliver(boolean steer) {
        JSONObject activeTurn = NativeChatState.runningTurn(chat);
        if (!writable() || delivering() || !queueingDraft(activeTurn)) return;
        captureDraft();
        final Draft submitted = draft();
        if (NativeSlashCommand.parse(submitted.text) != null) {
            goalError = ui.t("Wait for the running response before using agent commands"); updateComposer(); return;
        }
        final String target = chatID, message = submitted.text, auth = csrf, loc = locale();
        final List<Attachment> files = new ArrayList<>(submitted.files);
        final Client captured = client;
        final int token = generation, session = authEpoch, selected = selectionEpoch;
        final Delivery local = new Delivery(serverProfiles.selected, user.optString("id"), target, activeTurn.optString("id"), message, files, steer);
        localDeliveries.add(local);
        final JSONObject payload = object("message", message, "backend", chat.optString("backend", backend), "model", model,
                "reasoning_effort", effort, "permission_mode", permission, "delivery", steer ? "steer" : "queue", "client_message_id", local.id);
        if (remoteAgents != null) remoteAgents.attachMCPs(payload);
        beginDelivery(true); goalError = "";
        submitted.text = ""; submitted.files.clear(); composer.setText("");
        renderFiles(); renderChat();
        io.execute(() -> {
            try {
                if (token != generation || session != authEpoch || destroyed) return;
                Object value;
                if (files.isEmpty()) value = request(captured, "/chats/" + target + "/messages", payload, auth, loc);
                else {
                    JSONArray upload = new JSONArray();
                    for (Attachment file : files)
                        upload.put(object("name", file.name, "data", android.util.Base64.encodeToString(file.data, android.util.Base64.NO_WRAP)));
                    value = decodeResponse(captured.upload(target, object("fields", uploadFields(payload), "files", upload).toString(), auth, loc));
                }
                final JSONObject result = (JSONObject) value;
                post(token, () -> {
                    if (session != authEpoch) return;
                    delivering = false;
                    localDeliveries.remove(local);
                    if (selected == selectionEpoch && target.equals(chatID) && !result.optString("id").isEmpty()) {
                        chat = NativeChatState.reconcile(chat, result); renderChat();
                    }
                    updateComposer();
                });
            } catch (Exception e) {
                post(token, () -> {
                    if (session != authEpoch) return;
                    delivering = false;
                    if (e instanceof ApiFailure && ((ApiFailure) e).status == 401) { expire(); return; }
                    if (selected == selectionEpoch && target.equals(chatID)) {
                        if (localDeliveries.contains(local)) goalError = e.getMessage() == null ? e.toString() : e.getMessage();
                    }
                    local.status = "failed";
                    renderChat();
                });
            }
        });
    }

    private void queueAction(String id, String action) {
        queueAction(id, action, "");
    }

    private void queueAction(String id, String action, String before) {
        if (!writable() || delivering() || chat == null || chatID.isEmpty()) return;
        Delivery local = localDelivery(id);
        if (local != null) {
            if (action.equals("delete") && local.status.equals("failed")) { localDeliveries.remove(local); renderChat(); }
            return;
        }
        final String target = chatID;
        final int selected = selectionEpoch;
        beginDelivery(false); goalError = ""; updateComposer();
        api("/chats/" + Uri.encode(target) + "/queue", object("id", id, "action", action, "before_id", before), value -> {
            delivering = false;
            if (selected == selectionEpoch && target.equals(chatID)) { chat = NativeChatState.reconcile(chat, (JSONObject) value); renderChat(); }
            updateComposer();
        }, failure -> {
            delivering = false;
            if (selected == selectionEpoch && target.equals(chatID)) goalError = failure.getMessage() == null ? failure.toString() : failure.getMessage();
            updateComposer();
        });
    }

    private void editQueuedMessage(String id) {
        if (!writable() || delivering() || chat == null || chatID.isEmpty()) return;
        Delivery local = localDelivery(id);
        if (local != null) { if (local.status.equals("failed")) editDelivery(local); return; }
        captureDraft();
        final String target = chatID, path = "/chats/" + Uri.encode(chatID) + "/queue";
        final int selected = selectionEpoch;
        beginDelivery(false); editingQueue = true; goalError = ""; updateComposer();
        java.util.function.Consumer<Exception> failed = failure -> {
            delivering = false; editingQueue = false;
            if (target.equals(chatID) && selected == selectionEpoch) goalError = failure.getMessage();
            updateComposer();
        };
        api(path, object("id", id, "action", "draft"), value -> {
            if (!target.equals(chatID) || selected != selectionEpoch) { delivering = false; updateComposer(); return; }
            final Draft restored = new Draft();
            try {
                JSONObject data = (JSONObject) value;
                restored.text = data.optString("message");
                JSONArray files = array(data, "files");
                for (int i = 0; i < files.length(); i++) {
                    JSONObject file = files.getJSONObject(i);
                    restored.files.add(new Attachment(file.getString("name"), file.getString("media_type"),
                            android.util.Base64.decode(file.getString("data"), android.util.Base64.DEFAULT)));
                }
                int bytes = 0;
                for (Attachment file : draft().files) bytes += file.data.length;
                for (Attachment file : restored.files) bytes += file.data.length;
                if (draft().files.size() + restored.files.size() > 6 || bytes > 16 * 1024 * 1024)
                    throw new IOException(ui.t("Attach at most 6 files, up to 16 MB total"));
            } catch (Exception error) { failed.accept(error); return; }
            api(path, object("id", id, "action", "edit"), next -> {
                delivering = false; editingQueue = false;
                editedQueueDrafts.put(target, restored);
                if (target.equals(chatID)) {
                    chat = NativeChatState.reconcile(chat, (JSONObject) next); renderChat();
                }
                updateComposer();
            }, failed);
        }, failed);
    }

    private void sendOrStop() {
        if (!writable()) return;
        if (remoteAgents != null && remoteAgents.menuSubmission()) return;
        if (remoteAgents != null && remoteAgents.targeted()) {
            captureDraft();
            final Draft submitted = draft(); final String message = submitted.text;
            if (NativeGoalCommand.trim(message).isEmpty()) return;
            goalNotice = goalError = "";
            remoteAgents.send(message, !submitted.files.isEmpty(), accepted -> {
                if (accepted && composer != null && composer.getText().toString().equals(message)) {
                    submitted.text = ""; composer.setText("");
                }
            });
            return;
        }
        JSONObject activeTurn = NativeChatState.runningTurn(chat);
        if (activeTurn != null) {
            if (!NativeGoalCommand.trim(composer.getText().toString()).isEmpty() || !draft().files.isEmpty()) deliver(false);
            else stopResponse(activeTurn);
            return;
        }
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
        NativeSlashCommand.Input slash = NativeSlashCommand.parse(submitted.text);
        NativeSlashCommand definition = slash == null ? null : NativeSlashCommand.find(NativeSlashCommand.catalog(agentCommands()), slash.name);
        if (slash != null && !submitted.files.isEmpty()) {
            goalError = ui.t("Agent commands do not accept attachments"); updateComposer(); return;
        }
        if (slash != null && definition == null) {
            String unavailable = commandCatalogMessage();
            goalError = unavailable.isEmpty() ? ui.t("No matching agent commands") : unavailable; updateComposer(); return;
        }
        if (command != null) {
            String issue = command.validationError(!submitted.files.isEmpty());
            if (!issue.isEmpty()) { goalError = ui.t(issue); updateComposer(); return; }
            if (!command.action.equals("set")) { runGoalControl(command.action, submitted); return; }
        }
        if (definition != null && definition.kind.equals("client")) {
            runSessionCommand(definition.name, slash.arguments, submitted); return;
        }
        if (definition != null && definition.kind.equals("control") && command == null && !(definition.name.equals("plan") && !slash.arguments.isEmpty())) {
            runAgentControl("/" + definition.name + (slash.arguments.isEmpty() ? "" : " " + slash.arguments), submitted); return;
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
        if (remoteAgents != null) remoteAgents.attachMCPs(payload);
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
                                    if (remoteAgents != null) remoteAgents.adoptChat(created.optString("id"));
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
                                                    object("fields", uploadFields(payload), "files", upload)
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
                                            if (remoteAgents != null) remoteAgents.adoptChat(result.optString("id"));
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

    private void clearCommandDraft(Draft submitted, NativeGoalRequest request) {
        if (request.clears(submitted.text)) submitted.text = "";
        if (composer != null && request.clears(composer.getText().toString())) composer.setText("");
    }

    private void commandFailed(Exception failure) {
        goalError = failure.getMessage() == null ? failure.toString() : failure.getMessage(); updateComposer();
    }

    private JSONObject uploadFields(JSONObject payload) throws org.json.JSONException {
        JSONObject fields = new JSONObject(payload.toString());
        JSONArray mcp = fields.optJSONArray("mcp_servers");
        if (mcp != null) fields.put("mcp_servers", mcp.toString());
        return fields;
    }

    private void runAgentControl(String command, Draft submitted) {
        if (chat != null && !backend.equals(chat.optString("backend"))) {
            goalError = ui.t("Select this chat's current agent to run session commands"); updateComposer(); return;
        }
        NativeGoalRequest origin = new NativeGoalRequest(authEpoch, selectionEpoch, selectionKey(), submitted.text);
        JSONObject options = object("command", command, "model", model, "reasoning_effort", effort, "permission_mode", permission);
        goalNotice = goalError = ""; busy = true; updateComposer();
        if (!chatID.isEmpty()) { sendAgentControl(options, submitted, origin); return; }
        final String pid = projectID, draftKey = selectionKey(), message = submitted.text;
        api("/chats", object("project_id", pid, "backend", backend), value -> {
            if (!origin.current(authEpoch, selectionEpoch, selectionKey())) { busy = false; updateComposer(); return; }
            JSONObject created = (JSONObject) value;
            if (created.optString("id").isEmpty()) {
                busy = false; commandFailed(new IOException(ui.t("The server returned an invalid response"))); return;
            }
            captureDraft();
            if (remoteAgents != null) remoteAgents.adoptChat(created.optString("id"));
            chatID = created.optString("id"); chat = created;
            drafts.remove(draftKey); drafts.put(chatID, submitted); ++selectionEpoch;
            chats = NativeChatState.reconcileList(chats, created);
            NativeGoalRequest adopted = new NativeGoalRequest(authEpoch, selectionEpoch, selectionKey(), message);
            renderChat(); startChat(); sendAgentControl(options, submitted, adopted);
        }, failure -> {
            busy = false;
            if (origin.current(authEpoch, selectionEpoch, selectionKey())) commandFailed(failure); else updateComposer();
        });
    }

    private void sendAgentControl(JSONObject options, Draft submitted, NativeGoalRequest origin) {
        api("/chats/" + Uri.encode(chatID) + "/commands", options, value -> {
            busy = false;
            if (!origin.current(authEpoch, selectionEpoch, selectionKey())) { updateComposer(); return; }
            JSONObject result = (JSONObject) value;
            model = result.optString("model"); effort = result.optString("reasoning_effort"); permission = result.optString("permission_mode");
            NativeChatState.put(chat, "model", model); NativeChatState.put(chat, "reasoning_effort", effort); NativeChatState.put(chat, "permission_mode", permission);
            goalNotice = result.optString("message"); clearCommandDraft(submitted, origin);
            renderChat(); loadPermissions();
        }, failure -> {
            busy = false;
            if (origin.current(authEpoch, selectionEpoch, selectionKey())) commandFailed(failure); else updateComposer();
        });
    }

    private void runSessionCommand(String name, String argument, Draft submitted) {
        NativeGoalRequest origin = new NativeGoalRequest(authEpoch, selectionEpoch, selectionKey(), submitted.text);
        goalNotice = goalError = "";
        if (name.equals("clear") || name.equals("new")) {
            if (!argument.isEmpty()) { goalError = "Usage: /" + name; updateComposer(); return; }
            if (canCreateInProject()) { clearCommandDraft(submitted, origin); newChat(projectID); }
            return;
        }
        if (name.equals("agents") || name.equals("resume")) {
            if (argument.isEmpty()) { clearCommandDraft(submitted, origin); openSidebar(); return; }
            for (int i = 0; i < chats.length(); i++) {
                JSONObject target = at(chats, i);
                if (target.optString("backend").equals(backend) && (argument.equals(target.optString("id")) || argument.equals(target.optString("title")))) {
                    clearCommandDraft(submitted, origin); openChat(target.optString("id")); return;
                }
            }
            goalError = ui.t("Conversation not found"); updateComposer(); return;
        }
        if (chat == null || !backend.equals(chat.optString("backend"))) {
            goalError = ui.t("Select this chat's current agent to run session commands"); updateComposer(); return;
        }
        if (name.equals("rename") && argument.isEmpty()) {
            goalError = ui.t("Enter a conversation name"); updateComposer(); return;
        }
        if (!name.equals("rename") && !argument.isEmpty()) { goalError = "Usage: /" + name; updateComposer(); return; }
        if (!name.equals("rename") && !name.equals("archive") && !name.equals("delete") && !name.equals("fork")) {
            goalError = ui.t("No matching agent commands"); updateComposer(); return;
        }
        String target = chatID;
        JSONObject body = name.equals("rename") ? object("title", argument) : name.equals("fork")
                ? object("backend", backend, "model", model, "reasoning_effort", effort) : object();
        busy = true; updateComposer();
        api("/chats/" + Uri.encode(target) + "/" + name, body, value -> {
            busy = false;
            if (!origin.current(authEpoch, selectionEpoch, selectionKey())) { updateComposer(); return; }
            clearCommandDraft(submitted, origin);
            if (workspaceData != null) workspaceData.navigationChanged();
            if (name.equals("rename")) {
                chat = NativeChatState.metadata(chat, (JSONObject) value);
                for (int i = 0; i < chats.length(); i++) if (target.equals(at(chats, i).optString("id")))
                    chats.put(i, NativeChatState.metadata(at(chats, i), (JSONObject) value));
                heading.setText(chatTitle()); updateComposer();
            } else if (name.equals("fork")) {
                chats = NativeChatState.reconcileList(chats, (JSONObject) value); openChat(((JSONObject) value).optString("id"));
            } else acceptNavigation((JSONObject) value, target);
        }, failure -> {
            busy = false;
            if (origin.current(authEpoch, selectionEpoch, selectionKey())) commandFailed(failure); else updateComposer();
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

    private boolean canAddAttachments() {
        return writable() && !busy && !(delivering() && editingQueue)
                && (remoteAgents == null || !remoteAgents.busy() && !remoteAgents.targeted());
    }

    private void pickFiles() {
        if (!canAddAttachments()) return;
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
        if (NativeProjectUpload.handles(requestCode)) {
            if (projectUpload != null) projectUpload.result(requestCode, resultCode, data);
            return;
        }
        if (NativeDesktopDocuments.handles(requestCode)) {
            if (desktopDialog != null) desktopDialog.documents.result(requestCode, resultCode, data);
            return;
        }
        if (requestCode == SCAN_PAIRING) {
            String owner = pairingOwner; pairingOwner = "";
            String contents = data == null ? null : data.getStringExtra(com.google.zxing.client.android.Intents.Scan.RESULT);
            if (resultCode != RESULT_OK || contents == null || owner.isEmpty()
                    || !owner.equals(serverProfiles.selected) && (newServer == null || !owner.equals(newServer.id))) return;
            try {
                RelayPairing pairing = new RelayPairing(contents);
                if (!pairing.invitation) throw new IllegalArgumentException(tr("Scan a new code in Spool Desktop.", "请扫描 Spool Desktop 新生成的二维码。"));
                ConnectionPlan previous = newServer == null ? connectionPlan : connectionSettings.plan();
                ConnectionPlan plan = new ConnectionPlan(previous.direct, previous.tailscale, pairing.code, ConnectionMode.RELAY, previous.autoSwitch);
                if (connectionSettings != null) {
                    connectionSettings.input(ConnectionMode.RELAY).setText(pairing.code);
                    connectionSettings.show(ConnectionMode.RELAY);
                }
                if (newServer != null && !persistNewServer(plan)) return;
                newServer = null;
                if (serverSettings != null) serverSettings.commitName();
                applyConnectionPlan(plan, ConnectionMode.RELAY);
            } catch (IllegalArgumentException error) { toast(error); }
            return;
        }
        if (requestCode == NativeAppUpdate.INSTALL_PERMISSION) { appUpdates.permissionResult(this); return; }
        if (requestCode == SAVE_FILE) {
            PendingDownload selected = pendingDownload;
            pendingDownload = null;
            downloading = false;
            if (selected == null || selected.session != authEpoch) return;
            if (resultCode != RESULT_OK) { downloadNotice("Download cancelled"); return; }
            if (data == null || data.getData() == null) { downloadFailed(selected.name, new IOException("No save destination selected")); return; }
            Uri destination = data.getData();
            if (!"content".equals(destination.getScheme())) { downloadFailed(selected.name, new IOException("Only document-provider destinations are supported")); return; }
            downloading = true;
            int token = generation, request = ++downloadRequest;
            downloadNotice(selected.file == null ? "Downloading {name}…" : "Saving {name}…", "name", selected.name);
            String downloadLocale = locale();
            io.execute(() -> {
                if (token != generation || selected.session != authEpoch || request != downloadRequest) return;
                try {
                    try (OutputStream output = getContentResolver().openOutputStream(destination, "wt")) {
                        if (output == null) throw new IOException("Cannot write file");
                        if (selected.file != null) output.write(selected.file.bytes);
                        else {
                            spoolnet.ProjectFiles transfer = selected.client.openProjectFiles();
                            activeDownload.set(transfer);
                            try {
                                if (token != generation || selected.session != authEpoch || request != downloadRequest) return;
                                decodeResponse(transfer.download(selected.path, downloadLocale, chunk -> {
                                    if (destroyed || token != generation || selected.session != authEpoch || request != downloadRequest)
                                        throw new IOException("Download cancelled");
                                    output.write(chunk);
                                }));
                            } finally {
                                transfer.close();
                                activeDownload.compareAndSet(transfer, null);
                            }
                        }
                    }
                    String location = NativeDownloadDestination.describe(this, destination, selected.name);
                    post(token, () -> {
                        if (selected.session != authEpoch || request != downloadRequest) return;
                        downloading = false;
                        downloadNotice("Downloaded to {path}", "path", location);
                    });
                } catch (Exception error) { post(token, () -> {
                    if (selected.session != authEpoch || request != downloadRequest) return;
                    downloading = false;
                    downloadFailed(selected.name, error);
                    if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire();
                }); }
            });
            return;
        }
        if (requestCode != PICK_FILES) return;
        boolean selecting = pickerDraft != null;
        pickerDraft = null;
        if (resultCode != RESULT_OK || data == null || !selecting) return;
        List<Uri> uris = new ArrayList<>();
        ClipData clips = data.getClipData();
        if (clips != null)
            for (int i = 0; i < clips.getItemCount(); i++)
                uris.add(clips.getItemAt(i).getUri());
        else if (data.getData() != null) uris.add(data.getData());
        addAttachmentUris(uris, () -> {});
    }

    private void addAttachmentUris(List<Uri> uris, Runnable release) {
        if (!canAddAttachments() || uris.isEmpty() || uris.size() + draft().files.size() > 6) {
            release.run();
            if (uris.size() + draft().files.size() > 6)
                toast(new IOException(ui.t("Attach at most 6 files, up to 16 MB total")));
            return;
        }
        final String key = selectionKey();
        final String selectedProject = projectID;
        final int version = draftVersion;
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
                                            || !selectedProject.equals(projectID) || !key.equals(selectionKey())
                                            || !canAddAttachments()) return;
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
                    } finally {
                        release.run();
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
            public void failed() {
                if (client != captured) return;
                ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
                if (runtime != null) runtime.socketFailed = true;
                updateConnectionSettings(); renderNavigation();
                connectionFailed(captured);
            }
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
        ServerRuntime runtime = serverRuntimes.get(serverProfiles.selected);
        if (value && runtime != null) runtime.socketFailed = false;
        if (workspaceData != null) workspaceData.ready(value);
        if (!value) {
            setDesktopAvailable(false);
            presenceStale = true;
            disconnectPeople(); updateHeaderPeople(); updateMissingProfiles();
        } else renderPeople();
        if (value) { updateConnectionSettings(); renderNavigation(); }
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
        if (!java.util.Arrays.asList("navigation", "agents", "profiles", "presence", "chat_groups", "direct_message", "group_message", "direct_message_error", "group_result", "desktop", "server", "session_notification", "federation_targets").contains(kind)) return;
        Object value = new JSONTokener(raw).nextValue();
        applyWorkspaceEvent(kind, value, epoch);
    }

    private void decodeWorkspaceEvent(String kind, String raw, int epoch) {
        if (kind.equals("ready") || kind.equals("transport_error") || kind.equals("auth_expired")) {
            try { workspaceEvent(kind, raw, epoch); } catch (Exception error) { toast(error); }
            return;
        }
        if (!java.util.Arrays.asList("navigation", "agents", "profiles", "presence", "chat_groups", "direct_message", "group_message", "direct_message_error", "group_result", "desktop", "server", "session_notification", "federation_targets").contains(kind)) return;
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
            case "session_notification":
                if (value instanceof JSONObject && sessionNotifications.receive((JSONObject) value, user.optString("id"), viewedNotificationChat())) renderNavigation();
                break;
            case "server":
                rememberServerName(serverProfiles.active(), (JSONObject) value);
                renderNavigation();
                break;
            case "federation_targets":
                if (value instanceof JSONArray && !routeLoginRequired) {
                    federationTargets = (JSONArray) value;
                    rememberFederationTargets(serverRuntimes.get(serverProfiles.selected), user.optString("id"), federationTargets);
                    updateComposer();
                }
                break;
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
            return NativeAvatars.name(user);
        if (id.equals("online-room")) return tr("Online group chat", "在线群聊");
        for (int i = 0; i < groups.length(); i++)
            if (at(groups, i).optString("id").equals(id)) return at(groups, i).optString("name");
        for (int i = 0; i < profiles.length(); i++)
            if (at(profiles, i).optString("id").equals(id))
                return NativeAvatars.name(at(profiles, i));
        return tr("Workspace member", "工作空间成员");
    }

    private String viewedNotificationChat() {
        return foreground && hasWindowFocus() && !sidebarPageDeferred ? chatID : "";
    }

    private void readSessionNotification() {
        if (sessionNotifications.dismiss(viewedNotificationChat())) renderNavigation();
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
                for (String field : new String[] {"source_server_id", "source_server_name"})
                    user.put(field, profile.opt(field));
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
        openFiles(path, true);
    }

    private void openFiles(String path, boolean animate) {
        openFiles(path, animate, null);
    }

    private void openFiles(String path, boolean animate, NativeProjectUpload retainedUpload) {
        closeDrawer();
        panel = "files";
        NativeDrawer overlay = new NativeDrawer(ui, true, this::closeDrawer);
        drawer = overlay;
        if (fileState == null || !fileState.project.equals(projectID) || !fileState.chat.equals(chatID)) {
            fileState = new NativeFileModel(projectID, chatID);
            fileState.split = getPreferences(MODE_PRIVATE).getFloat("fileTreeFraction", .32f);
        }
        NativeFileModel ownerFiles = fileState;
        Client fileClient = client;
        fileBrowser = new NativeFileBrowser(ui, projectID.isEmpty() ? "" : projectName(), fileState, fileImages,
                (viewUi, apiPath) -> new NativePdfPreview(viewUi, imageWorker, address -> loadBinary(fileClient, address), apiPath),
                this::readProjectFile, file -> downloadFile(NativeFileData.imageAction(ownerFiles.project, ownerFiles.chat, file, "download", ownerFiles.workspacePath()), file.optString("name")),
                this::openExternalLink, this::closeDrawer,
                value -> getPreferences(MODE_PRIVATE).edit().putFloat("fileTreeFraction", (float) value).apply());
        fileBrowser.gitPanel.write = this::requestProjectFile;
        if (retainedUpload != null && retainedUpload.rebind(ui, fileBrowser)) projectUpload = retainedUpload;
        else {
            int uploadGeneration = generation, uploadAuth = authEpoch, uploadSelection = selectionEpoch;
            String uploadCSRF = csrf, uploadLocale = locale();
            projectUpload = new NativeProjectUpload(this, ui, fileBrowser, io, () -> {
                if (fileClient == null) throw new IOException("No file server configured");
                spoolnet.ProjectFiles files = fileClient.openProjectFiles();
                return new NativeProjectUpload.Transport() {
                    public void upload(String apiPath, String name, InputStream input) throws Exception {
                        decodeResponse(files.upload(apiPath, name, uploadCSRF, uploadLocale, maximum -> {
                            byte[] buffer = new byte[(int) maximum]; int count = input.read(buffer);
                            return count < 0 ? new byte[0] : java.util.Arrays.copyOf(buffer, count);
                        }));
                    }
                    public void close() { files.close(); }
                };
            }, () -> !destroyed && user != null && !routeLoginRequired && generation == uploadGeneration
                    && authEpoch == uploadAuth && selectionEpoch == uploadSelection && client == fileClient && fileState == ownerFiles,
                    this::canUploadFiles,
                    error -> { if (error instanceof ApiFailure && ((ApiFailure) error).status == 401) expire(); });
        }
        NativeProjectUpload ownerUpload = projectUpload;
        fileBrowser.canUpload = this::canUploadFiles;
        fileBrowser.upload = ownerUpload::pick;
        fileBrowser.uploadScopeChanged = ownerUpload::cancel;
        overlay.sheet.addView(fileBrowser, new LinearLayout.LayoutParams(-1, -1));
        root.addView(overlay, new FrameLayout.LayoutParams(-1, -1));
        overlay.show(animate);
        fileBrowser.start();
    }

    private void readProjectFile(String path, NativeFileBrowser.Completion completion) {
        requestProjectFile(path, null, completion);
    }

    private void requestProjectFile(String path, JSONObject body, NativeFileBrowser.Completion completion) {
        Client captured = client;
        int token = generation, session = authEpoch;
        String auth = csrf, loc = locale();
        io.execute(() -> {
            try {
                if (token != generation || session != authEpoch) return;
                JSONObject value = (JSONObject) request(captured, path.substring(4), body, auth, loc);
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

    private NativeFileData loadBinary(Client captured, String path) throws Exception {
        if (!NativeFileData.allowedPath(path)) throw new IOException("Invalid file API path");
        if (captured == null) throw new IOException("No file server configured");
        JSONObject response = new JSONObject(captured.request("GET", path, "", "", ""));
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
        previewImage(NativeImageResource.local(file.name, file.id, file.file));
    }

    private void previewImage(NativeImageResource resource) {
        if (resource == null) { toast(new IOException(tr("File unavailable", "文件不可用"))); return; }
        final int session = authEpoch;
        final Client captured = client;
        NativeImagePreview preview = showImagePreview(resource.name, () -> {
            if (resource.local == null && !NativeFileData.inlineImage(resource.source) && !NativeFileData.toolImagePath(resource.source)) {
                Uri uri = Uri.parse(resource.source);
                if (uri.getPath().endsWith("/file")) {
                    Uri.Builder original = uri.buildUpon().clearQuery();
                    for (String key : uri.getQueryParameterNames()) if (!key.equals("image") && !key.equals("download")) original.appendQueryParameter(key, uri.getQueryParameter(key));
                    downloadFile(original.appendQueryParameter("download", "1").build().toString(), resource.name);
                } else downloadFile(resource.source, resource.name);
                return;
            }
            if (downloading) return;
            downloading = true;
            downloadNotice("Downloading {name}…", "name", resource.name);
            int token = generation, request = ++downloadRequest;
            io.execute(() -> {
                try {
                    NativeFileData data = resource.read(path -> loadBinary(captured, path));
                    post(token, () -> {
                        if (session != authEpoch || request != downloadRequest) return;
                        downloading = false; chooseDownload(data, resource.name, session);
                    });
                } catch (Exception error) { post(token, () -> {
                    if (session != authEpoch || request != downloadRequest) return;
                    downloading = false; downloadFailed(resource.name, error);
                }); }
            });
        });
        final int selected = selectionEpoch;
        preview.content.reload = () -> {
            if (!destroyed && user != null && session == authEpoch && selected == selectionEpoch && preview.isShowing())
                resource.load(fileImages, preview.content.image, previewEdge(), preview.completion());
        };
        preview.content.reload.run();
    }

    private void closeImagePreview() {
        NativeImagePreview preview = imagePreview;
        imagePreview = null;
        if (preview != null) preview.dismiss();
    }

    private void downloadFile(String path, String name) {
        if (downloading) return;
        if (!NativeFileData.allowedPath(path) || client == null) { downloadFailed(name, new IOException("File unavailable")); return; }
        chooseDownload(new PendingDownload(null, name, path, client, authEpoch), "application/octet-stream");
    }

    private void closeActiveDownload() {
        // Detach before closing: the IO worker can finish while the UI cancels.
        spoolnet.ProjectFiles transfer = activeDownload.getAndSet(null);
        if (transfer != null) transfer.close();
    }

    private void chooseDownload(NativeFileData file, String name, int session) {
        chooseDownload(new PendingDownload(file, name, "", null, session), file.mediaType);
    }

    private void chooseDownload(PendingDownload selected, String mediaType) {
        if (selected.session != authEpoch || downloading || destroyed || !foreground) return;
        String filename = selected.name.replaceAll("[\\\\/\\p{Cntrl}]", "_");
        if (filename.isEmpty()) filename = "attachment";
        pendingDownload = new PendingDownload(selected.file, filename, selected.path, selected.client, selected.session);
        downloading = true;
        try {
            startActivityForResult(new Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
                    .setType(mediaType).putExtra(Intent.EXTRA_TITLE, filename), SAVE_FILE);
        } catch (RuntimeException error) {
            pendingDownload = null;
            downloading = false;
            downloadFailed(filename, error);
        }
    }

    private void downloadNotice(String key, Object... values) {
        if (downloadToast != null) downloadToast.cancel();
        downloadToast = Toast.makeText(this, NativeText.translate(key, locale(), values), Toast.LENGTH_LONG);
        downloadToast.show();
    }

    private void downloadFailed(String name, Throwable error) {
        String detail = error.getMessage();
        if (detail == null || detail.isEmpty()) detail = NativeText.translate("Request failed", locale());
        downloadNotice("Could not download {name}: {error}", "name", name, "error", detail);
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

    private AdbShare adbShare() {
        if (adbShare != null) return adbShare;
        AdbSession session = adbSessions.computeIfAbsent(serverProfiles.selected, key -> new AdbSession(serverProfiles.active()));
        return adbShare = session.share;
    }

    private void selectAdbSession() {
        AdbSession session = adbSessions.get(serverProfiles.selected);
        adbShare = session == null ? null : session.share;
        adbAvailability = null;
        adbPermissionSession = -1;
    }

    private void removeAdbSession(ServerProfiles.Server server) {
        AdbSession session = adbSessions.get(server.id);
        if (session == null) return;
        session.share.stop();
        session.discovery.close();
        adbSessions.remove(server.id);
        if (adbShare == session.share) adbShare = null;
    }

    /** Owns its server, login and discovery independently of the visible workspace. */
    private final class AdbSession implements AdbShareService.Controls {
        final ServerProfiles.Server server;
        final AdbDiscovery discovery = new AdbDiscovery(MainActivity.this);
        final AdbShare share;
        String account = "";

        AdbSession(ServerProfiles.Server server) {
            this.server = server;
            share = new AdbShare(new AdbShare.Host() {
                public void port(boolean pairing, java.util.function.IntConsumer found) {
                    discovery.find(pairing ? AdbDiscovery.PAIRING : AdbDiscovery.CONNECT, found);
                }
                public void cancelDiscovery() { discovery.close(); }
                private AdbShare.Link open(boolean pairing, String code, int port, AdbShare.Events events) throws Exception {
                    ConnectionRouter.Session login = login();
                    Client captured = transport();
                    if (captured == null || login == null) throw new IOException("Not connected");
                    String auth = new JSONObject(login.json).getString("csrf");
                    spoolnet.EventListener listener = (kind, data) -> {
                        String value = "", text = "";
                        try {
                            JSONObject fields = new JSONObject(data);
                            value = fields.optString(kind.equals("ready") ? "serial" : kind.equals("state") ? "state" : "code");
                            text = fields.optString("message");
                        } catch (Exception ignored) {}
                        final String first = value, second = text;
                        main.post(() -> { if (!destroyed) events.event(kind, first, second); });
                    };
                    spoolnet.ADBStream stream = pairing ? captured.pairADB(auth, code, port, listener)
                            : captured.shareADB(auth, adbDeviceKey(), port, listener);
                    return () -> {
                        try { streamCloser.execute(stream::close); }
                        catch (java.util.concurrent.RejectedExecutionException ignored) { stream.close(); }
                    };
                }
                public AdbShare.Link share(int port, AdbShare.Events events) throws Exception { return open(false, "", port, events); }
                public AdbShare.Link pair(String code, int port, AdbShare.Events events) throws Exception { return open(true, code, port, events); }
                public void availability(java.util.function.Consumer<String> result) {
                    ConnectionRouter.Session login = login();
                    Client captured = transport();
                    if (captured == null || login == null) {
                        if (available()) probeServer(server, true);
                        result.accept(null); return;
                    }
                    try {
                        String auth = new JSONObject(login.json).getString("csrf");
                        readAdbAvailability(captured, auth, () -> currentLogin(captured, auth), result);
                    } catch (Exception error) { result.accept(null); }
                }
                public void later(Runnable task, long millis) { main.postDelayed(task, millis); }
                public void cancel(Runnable task) { main.removeCallbacks(task); }
                public long now() { return android.os.SystemClock.elapsedRealtime(); }
                public double random() { return Math.random(); }
            }, this::changed);
        }

        private boolean available() {
            return !destroyed && server.enabled && serverProfiles.entries.contains(server);
        }

        private Client transport() {
            if (!available()) return null;
            if (server.id.equals(serverProfiles.selected)) return client;
            ServerRuntime runtime = serverRuntimes.get(server.id);
            return runtime == null || runtime.candidate == null ? null : runtime.candidate.client;
        }

        private ConnectionRouter.Session login() {
            if (!available()) return null;
            if (server.id.equals(serverProfiles.selected)) {
                if (user == null || routeLoginRequired || csrf.isEmpty() || !account.equals(user.optString("id"))) return null;
                return new ConnectionRouter.Session(account, object("csrf", csrf).toString());
            }
            ServerRuntime runtime = serverRuntimes.get(server.id);
            ConnectionRouter.Session login = runtime == null || runtime.candidate == null ? null : runtime.candidate.session;
            return login != null && account.equals(login.userID) ? login : null;
        }

        private boolean currentLogin(Client captured, String auth) {
            ConnectionRouter.Session login = login();
            if (transport() != captured || login == null) return false;
            try { return auth.equals(new JSONObject(login.json).optString("csrf")); }
            catch (Exception error) { return false; }
        }

        public void stop() { share.stop(); }
        public void pair(String code) { share.pair(code, 0); }
        public void foregroundUnavailable() { share.foregroundUnavailable(); }
        public void notificationUnavailable() { share.notificationUnavailable(); }

        private void changed() {
            if (share.active()) {
                if (!AdbShareService.canNotify(MainActivity.this)) share.notificationUnavailable();
                else if (!AdbShareService.show(MainActivity.this, this, share, server.displayName(), locale())) share.foregroundUnavailable();
            } else AdbShareService.end(MainActivity.this, this);
            if (server.id.equals(serverProfiles.selected)) bindAdbSettings();
        }
    }

    private String adbDeviceKey() {
        String key = getPreferences(MODE_PRIVATE).getString("adbDevice", "");
        if (key.matches("android-[0-9a-f]{16}")) return key;
        key = "android-" + UUID.randomUUID().toString().replace("-", "").substring(0, 16);
        getPreferences(MODE_PRIVATE).edit().putString("adbDevice", key).apply();
        return key;
    }

    /** Reports "" when sharing is available, the reason when it is not, null when the server did not answer. */
    private void readAdbAvailability(java.util.function.Consumer<String> result) {
        final Client captured = client;
        final int token = generation, session = authEpoch;
        final String auth = csrf;
        if (captured == null || user == null || routeLoginRequired || destroyed) { result.accept(null); return; }
        readAdbAvailability(captured, auth, () -> token == generation && session == authEpoch, result);
    }

    private void readAdbAvailability(Client captured, String auth, java.util.function.BooleanSupplier current,
                                     java.util.function.Consumer<String> result) {
        final String loc = locale();
        try {
            io.execute(() -> {
                String answer = null;
                try {
                    JSONObject value = (JSONObject) request(captured, "/adb", null, auth, loc);
                    answer = value.optBoolean("available") ? "" : value.optString("reason", "unavailable");
                } catch (ApiFailure error) {
                    if (error.status == 401 || error.status == 403) answer = "not_authorized";
                } catch (Exception ignored) {}
                final String reason = answer;
                main.post(() -> { if (!destroyed) result.accept(current.getAsBoolean() ? reason : null); });
            });
        } catch (java.util.concurrent.RejectedExecutionException ignored) {}
    }

    private void openAdbDialog() {
        if (adbDialog != null || !foreground || destroyed || user == null || client == null) return;
        captureDraft(); closeSettings(); closeDrawer(); closePeople();
        adbDialog = new NativeDialog(ui, ui.t("ADB bridge"), ui.t("Close"));
        adbDialog.actions.addView(ui.quietButton(ui.t("Close"), this::closeAdbDialog));
        adbSettings = new NativeAdbSettings(ui, this::startAdbShare, this::stopAdbShare,
                (code, port) -> { if (adbShare != null) adbShare.pair(code, port); });
        adbDialog.body.addView(adbSettings);
        adbDialog.setOnDismissListener(dialog -> {
            adbDialog = null; adbSettings = null;
            if (sidebar == null) main.removeCallbacks(adbPoll);
        });
        bindAdbSettings();
        adbDialog.show();
        refreshAdbAvailability();
    }

    private void closeAdbDialog() {
        if (adbDialog != null) adbDialog.dismiss();
    }

    private void refreshAdbAvailability() {
        main.removeCallbacks(adbPoll);
        if ((adbSettings == null && sidebar == null) || destroyed || Build.VERSION.SDK_INT < 30) return;
        if (foreground && !adbAvailabilityBusy) {
            adbAvailabilityBusy = true;
            final int session = authEpoch;
            readAdbAvailability(reason -> {
                adbAvailabilityBusy = false;
                if (session != authEpoch) return;
                // An unanswered poll keeps what the server last said.
                if (reason != null || client == null) adbAvailability = reason;
                bindAdbSettings();
            });
        }
        main.postDelayed(adbPoll, 5000);
    }

    private void bindAdbSettings() {
        if (adbSettings != null) adbSettings.bind(adbShare(), adbAvailability, Build.VERSION.SDK_INT >= 30);
        if (sidebar != null) bindSidebarAdb(sidebar);
    }

    private void bindSidebarAdb(NativeSidebar target) {
        boolean active = adbShare != null && adbShare.phase != AdbShare.Phase.IDLE;
        boolean available = Build.VERSION.SDK_INT >= 30 && !"disabled".equals(adbAvailability)
                && (active || adbAvailability != null && !adbAvailability.equals("not_authorized"));
        target.adb(available, adbShare == null ? "IDLE" : adbShare.phase.name(),
                adbShare == null ? "" : NativeAdbSettings.status(adbShare, locale()), this::openAdbDialog);
    }

    private boolean canStartAdbShare() {
        return Build.VERSION.SDK_INT >= 30 && foreground && !destroyed && client != null
                && user != null && !routeLoginRequired && !csrf.isEmpty() && "".equals(adbAvailability);
    }

    private void startAdbShare(int port) {
        if (!canStartAdbShare()) return;
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            adbPermissionPort = port;
            adbPermissionSession = authEpoch;
            requestPermissions(new String[] {android.Manifest.permission.POST_NOTIFICATIONS}, ADB_NOTIFICATION_PERMISSION);
            return;
        }
        if (AdbShareService.canNotify(this)) beginAdbShare(port);
        else adbShare().notificationUnavailable();
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode != ADB_NOTIFICATION_PERMISSION) return;
        int session = adbPermissionSession, port = adbPermissionPort;
        adbPermissionSession = -1;
        if (session != authEpoch || !canStartAdbShare()) return;
        if (grantResults.length != 0 && grantResults[0] == android.content.pm.PackageManager.PERMISSION_GRANTED
                && AdbShareService.canNotify(this)) beginAdbShare(port);
        else adbShare().notificationUnavailable();
    }

    private void stopAdbShare() {
        adbPermissionSession = -1;
        if (adbShare != null) adbShare.stop();
    }

    private boolean sharingAdb(String serverID) {
        AdbSession session = adbSessions.get(serverID);
        return session != null && session.share.active();
    }

    private void beginAdbShare(int port) {
        AdbShare share = adbShare();
        AdbSession session = adbSessions.get(serverProfiles.selected);
        session.account = user.optString("id");
        share.start(port);
    }

    private void back() {
        if (adbDialog != null) {
            closeAdbDialog();
            return;
        }
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
        if (relayDiscovery != null) relayDiscovery.refresh();
        monitor.setActive(settingsOpen);
        recovery.setActive(true);
        // Network callbacks remain registered in the background. Returning alone
        // must not retire healthy Relay connections, including the ADB tunnel.
        main.post(connectionRetry);
        refreshServerConnections(true);
        if (user != null && !routeLoginRequired) {
            resumeWorkspace(true);
            if (workspaceData != null) bindWorkspaceData(false);
            startChat();
            updateHistoryLoading();
            if (!inbox.active.isEmpty() && peoplePanel == null && !settingsOpen) openPeople(inbox.active);
        }
        if (composer != null && remoteAgents != null) updateComposer();
    }

    @Override public void onWindowFocusChanged(boolean focused) {
        super.onWindowFocusChanged(focused);
        if (focused) { readSessionNotification(); resumeWorkspace(); }
    }

    @Override
    protected void onStop() {
        if (remoteAgents != null) remoteAgents.suspend();
        finishFederation();
        closeChatMenu();
        foreground = false;
        if (relayDiscovery != null) relayDiscovery.close();
        // Active ADB shares still need the result, including a route replacement or login expiry.
        if (!sharingAdb(serverProfiles.selected)) {
            if (connectionRouter != null) connectionRouter.cancel();
            if (routeSelecting) { client = null; connected = false; connecting = false; busy = false; }
            routeSelecting = false;
        }
        for (Map.Entry<String, ServerRuntime> entry : serverRuntimes.entrySet()) {
            ServerRuntime runtime = entry.getValue();
            if (runtime.federationSignIn != null) {
                runtime.federationSignIn = null;
                if (entry.getKey().equals(serverProfiles.selected)) sessionCheckBusy = false;
            }
            stopServerFeed(runtime);
            if (sharingAdb(entry.getKey())) continue;
            if (runtime.selecting) runtime.candidate = null;
            runtime.router.cancel(); runtime.selecting = false;
        }
        main.removeCallbacks(serverRefresh);
        // The explicit SAF round trip pauses capture and retains this view's token.
        // Other background transitions still close the desktop immediately.
        if (desktopDialog == null || !desktopDialog.documents.waiting()) closeDesktop();
        if (projectUpload != null && !projectUpload.waiting()) projectUpload.cancel();
        if (fileBrowser != null) fileBrowser.deactivate();
        if (historyScroll != null) historyScroll.suspend();
        if (markwonHistory != null) markwonHistory.stopScroll();
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
        if (remoteAgents != null) remoteAgents.reset();
        closeChatMenu();
        clearSidebarPreparation();
        if (devicePairingDialog != null) devicePairingDialog.dismiss();
        if (appUpdates != null) appUpdates.close();
        closeDesktop();
        setDesktopAvailable(false);
        for (AdbSession session : new ArrayList<>(adbSessions.values())) session.share.stop();
        closeAdbDialog();
        for (AdbSession session : adbSessions.values()) session.discovery.close();
        adbSessions.clear();
        if (fileBrowser != null) fileBrowser.deactivate();
        closeProjectUpload();
        fileBrowser = null;
        destroyed = true;
        if (historyScroll != null) historyScroll.dispose();
        if (transcriptViewport != null) transcriptViewport.dispose();
        if (markwonHistory != null) markwonHistory.dispose();
        ++generation;
        for (ServerRuntime runtime : serverRuntimes.values()) { stopServerFeed(runtime); runtime.router.stop(); }
        monitor.stop();
        recovery.stop();
        dismissSettingsDialog();
        if (appearanceDialog != null) { appearanceDialog.dismiss(); appearanceDialog = null; }
        closeAvatarPicker();
        closeImagePreview();
        detachPeople();
        stopStreams();
        main.removeCallbacksAndMessages(null);
        if (networkCallback != null) connectivity.unregisterNetworkCallback(networkCallback);
        if (relayDiscovery != null) relayDiscovery.close();
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
        closeActiveDownload();
        if (downloadToast != null) downloadToast.cancel();
        // Revoked image reads skip their work; let queued animation-surface cleanup finish.
        imageWorker.shutdown();
        syntax.close();
        codeWorker.shutdown();
        super.onDestroy();
    }
}
