package com.cloudgeek.spool.android;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.ClipData;
import android.content.Intent;
import android.graphics.Color;
import android.graphics.Typeface;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.webkit.CookieManager;
import android.webkit.HttpAuthHandler;
import android.webkit.PermissionRequest;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.SslErrorHandler;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;
import androidx.webkit.ProxyConfig;
import androidx.webkit.ProxyController;
import androidx.webkit.WebViewFeature;
import go.Seq;
import java.io.ByteArrayInputStream;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.text.DateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import org.json.JSONObject;
import spoolnet.Client;
import spoolnet.Spoolnet;

public final class MainActivity extends Activity {
    private static final int FILE_REQUEST = 1;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private final ExecutorService diagnosticWorker = Executors.newSingleThreadExecutor();
    private final ConnectionMonitor connectionMonitor = new ConnectionMonitor(diagnosticWorker, new ConnectionMonitor.Dispatcher() {
        @Override public void post(Runnable task) { main.post(task); }
        @Override public void postDelayed(Runnable task, long delay) { main.postDelayed(task, delay); }
        @Override public void remove(Runnable task) { main.removeCallbacks(task); }
    });
    private volatile int generation;
    private volatile boolean destroyed;
    private Client client; // Only accessed by the serial worker.
    private ConnectivityManager connectivity;
    private ConnectivityManager.NetworkCallback networkCallback;
    private FrameLayout root;
    private ScrollView connectionPage;
    private LinearLayout browserPage;
    private EditText serverInput;
    private TextView status;
    private TextView browserStatus;
    private TextView connectionDetails;
    private TextView browserConnectionDetails;
    private AlertDialog connectionDialog;
    private JSONObject connectionInfo;
    private String tailnetState = "Disconnected";
    private boolean diagnosticsUnavailable;
    private int httpsState = R.string.connection_not_checked;
    private int pageState = R.string.connection_not_open;
    private boolean pageLoadFailed;
    private Button connectButton;
    private Button loginButton;
    private Button cancelButton;
    private WebView web;
    private String loginURL = "";
    private boolean proxyConfigured;
    private ValueCallback<Uri[]> pendingFiles;

    @Override public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        Seq.setContext(getApplicationContext());
        connectivity = getSystemService(ConnectivityManager.class);
        createConnectionPage();
        networkCallback = new ConnectivityManager.NetworkCallback() {
            @Override public void onAvailable(Network network) { publishInterfaces(); }
            @Override public void onLost(Network network) { publishInterfaces(); }
            @Override public void onLinkPropertiesChanged(Network network, android.net.LinkProperties properties) {
                publishInterfaces();
            }
        };
        connectivity.registerDefaultNetworkCallback(networkCallback, main);
        if (Build.VERSION.SDK_INT >= 33) {
            getOnBackInvokedDispatcher().registerOnBackInvokedCallback(0, this::handleBack);
        }
    }

    private int dp(int value) { return Math.round(value * getResources().getDisplayMetrics().density); }

    private TextView label(String value, int size, int color) {
        TextView view = new TextView(this);
        view.setText(value);
        view.setTextSize(size);
        view.setTextColor(color);
        view.setPadding(0, dp(8), 0, dp(8));
        return view;
    }

    private void createConnectionPage() {
        root = new FrameLayout(this);
        root.setBackgroundColor(Color.rgb(243, 245, 238));
        if (Build.VERSION.SDK_INT >= 30) getWindow().setDecorFitsSystemWindows(false);
        root.setOnApplyWindowInsetsListener((view, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.ime());
                view.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            } else {
                view.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                        insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
            }
            return insets;
        });
        connectionPage = new ScrollView(this);
        connectionPage.setFillViewport(true);
        LinearLayout form = new LinearLayout(this);
        form.setOrientation(LinearLayout.VERTICAL);
        form.setPadding(dp(28), dp(44), dp(28), dp(28));
        form.addView(label(getString(R.string.prototype), 11, 0xFF526B60));
        TextView title = label("Spool", 48, 0xFF153D3B);
        title.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        form.addView(title);
        form.addView(label(getString(R.string.subtitle), 20, 0xFF153D3B));
        TextView fieldLabel = label(getString(R.string.server_label), 14, 0xFF314D43);
        LinearLayout.LayoutParams fieldSpace = new LinearLayout.LayoutParams(-1, -2);
        fieldSpace.topMargin = dp(36);
        form.addView(fieldLabel, fieldSpace);
        serverInput = new EditText(this);
        serverInput.setSingleLine(true);
        serverInput.setTextSize(15);
        serverInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        serverInput.setHint(R.string.server_hint);
        serverInput.setText(getPreferences(MODE_PRIVATE).getString("serverOrigin", ""));
        serverInput.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO);
        fieldLabel.setLabelFor(View.generateViewId());
        serverInput.setId(fieldLabel.getLabelFor());
        form.addView(serverInput, new LinearLayout.LayoutParams(-1, dp(60)));
        connectButton = new Button(this);
        connectButton.setText(R.string.connect);
        connectButton.setOnClickListener(view -> connect());
        form.addView(connectButton, new LinearLayout.LayoutParams(-1, dp(56)));
        loginButton = new Button(this);
        loginButton.setText(R.string.tailnet_login);
        loginButton.setVisibility(View.GONE);
        loginButton.setOnClickListener(view -> {
            if (OriginPolicy.isLoginURL(loginURL)) openBrowser(loginURL);
        });
        form.addView(loginButton, new LinearLayout.LayoutParams(-1, dp(56)));
        cancelButton = new Button(this);
        cancelButton.setText(R.string.disconnect);
        cancelButton.setVisibility(View.GONE);
        cancelButton.setOnClickListener(view -> disconnect());
        form.addView(cancelButton, new LinearLayout.LayoutParams(-1, dp(56)));
        status = label(getString(R.string.ready), 15, 0xFF314D43);
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        form.addView(status);
        form.addView(label(getString(R.string.connection_details), 14, 0xFF153D3B));
        connectionDetails = label("", 13, 0xFF314D43);
        connectionDetails.setMinLines(7);
        form.addView(connectionDetails);
        form.addView(label(getString(R.string.connection_note), 12, 0xFF66736A));
        updateConnectionDetails();
        TextView note = label(getString(R.string.note), 12, 0xFF66736A);
        LinearLayout.LayoutParams noteSpace = new LinearLayout.LayoutParams(-1, -2);
        noteSpace.topMargin = dp(40);
        form.addView(note, noteSpace);
        connectionPage.addView(form);
        root.addView(connectionPage);
        setContentView(root);
    }

    private void publishInterfaces() {
        if (destroyed || worker.isShutdown()) return;
        worker.execute(() -> {
            try {
                Spoolnet.updateInterfaces(NetworkSnapshot.read(connectivity));
            } catch (Exception e) {
                int token = generation;
                post(token, () -> status.setText(getString(R.string.network_failed, safeMessage(e))));
            }
        });
    }

    private void connect() {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)) {
            status.setText(R.string.proxy_unsupported);
            return;
        }
        final OriginPolicy policy;
        try {
            policy = new OriginPolicy(serverInput.getText().toString());
        } catch (IllegalArgumentException e) {
            serverInput.setError(e.getMessage());
            return;
        }
        int token = ++generation;
        connectionMonitor.stop();
        connectionInfo = null;
        diagnosticsUnavailable = false;
        tailnetState = "Starting";
        httpsState = R.string.connection_not_checked;
        pageState = R.string.connection_not_open;
        updateConnectionDetails();
        getPreferences(MODE_PRIVATE).edit().putString("serverOrigin", policy.origin).apply();
        serverInput.setEnabled(false);
        connectButton.setEnabled(false);
        loginButton.setVisibility(View.GONE);
        cancelButton.setVisibility(View.VISIBLE);
        status.setText(R.string.starting);
        worker.execute(() -> {
            closeClient();
            try {
                Spoolnet.updateInterfaces(NetworkSnapshot.read(connectivity));
                Client created = Spoolnet.newClient(new File(getNoBackupFilesDir(), "tsnet").getAbsolutePath(), policy.origin);
                if (generation != token || destroyed) {
                    created.close();
                    return;
                }
                client = created;
                post(token, () -> {
                    connectionMonitor.start(created::connectionInfo, snapshot -> {
                        if (destroyed || generation != token) return;
                        try {
                            connectionInfo = snapshot == null ? null : new JSONObject(snapshot);
                        } catch (Exception ignored) {
                            connectionInfo = null;
                        }
                        diagnosticsUnavailable = connectionInfo == null;
                        tailnetState = connectionInfo == null ? "Unknown" : connectionInfo.optString("state", "Unknown");
                        updateConnectionDetails();
                    });
                    poll(token, policy, false);
                });
            } catch (Exception e) {
                post(token, () -> fail(e));
            }
        });
    }

    private void poll(int token, OriginPolicy policy, boolean loginRequested) {
        if (generation != token || destroyed) return;
        worker.execute(() -> {
            if (client == null || generation != token) return;
            try {
                JSONObject result = new JSONObject(client.status());
                String state = result.getString("state");
                String authURL = result.optString("authURL");
                post(token, () -> { tailnetState = state; updateConnectionDetails(); });
                boolean requested = loginRequested;
                if ("NeedsLogin".equals(state) && !requested) {
                    client.login();
                    requested = true;
                }
                if ("Running".equals(state)) {
                    post(token, () -> {
                        status.setText(R.string.checking);
                        httpsState = R.string.connection_checking;
                        updateConnectionDetails();
                    });
                    client.probe();
                    String address = client.proxyAddress();
                    String username = client.proxyUsername();
                    String password = client.proxyPassword();
                    String realm = client.proxyRealm();
                    post(token, () -> openWeb(token, policy, address, username, password, realm));
                    return;
                }
                boolean nextRequested = requested;
                post(token, () -> {
                    loginURL = authURL;
                    loginButton.setVisibility(OriginPolicy.isLoginURL(authURL) ? View.VISIBLE : View.GONE);
                    status.setText("NeedsLogin".equals(state) ? getString(R.string.waiting_login)
                            : "NeedsMachineAuth".equals(state) ? getString(R.string.waiting_approval)
                            : getString(R.string.connecting, state));
                    main.postDelayed(() -> poll(token, policy, nextRequested), 1500);
                });
            } catch (Exception e) {
                post(token, () -> fail(e));
            }
        });
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void openWeb(int token, OriginPolicy policy, String proxyAddress, String username, String password, String realm) {
        status.setText(R.string.opening);
        httpsState = R.string.connection_verified;
        pageState = R.string.connection_loading;
        browserPage = new LinearLayout(this);
        browserPage.setOrientation(LinearLayout.VERTICAL);
        LinearLayout toolbar = new LinearLayout(this);
        toolbar.setGravity(android.view.Gravity.CENTER_VERTICAL);
        toolbar.setPadding(dp(12), 0, dp(8), 0);
        browserStatus = label(policy.host, 12, 0xFF153D3B);
        toolbar.addView(browserStatus, new LinearLayout.LayoutParams(0, -2, 1));
        Button details = new Button(this);
        details.setText(R.string.connection_details);
        details.setOnClickListener(view -> {
            connectionDialog = new AlertDialog.Builder(this).setTitle(R.string.connection_details)
                    .setMessage(connectionDetails.getText() + "\n\n" + getString(R.string.connection_note))
                    .setPositiveButton(R.string.close, null).create();
            connectionDialog.setOnDismissListener(dialog -> connectionDialog = null);
            connectionDialog.show();
        });
        toolbar.addView(details, new LinearLayout.LayoutParams(-2, dp(48)));
        Button disconnect = new Button(this);
        disconnect.setText(R.string.disconnect);
        disconnect.setOnClickListener(view -> disconnect());
        toolbar.addView(disconnect, new LinearLayout.LayoutParams(-2, dp(48)));
        browserPage.addView(toolbar);
        browserConnectionDetails = label("", 12, 0xFF526B60);
        browserConnectionDetails.setPadding(dp(12), 0, dp(12), dp(8));
        browserPage.addView(browserConnectionDetails);
        updateConnectionDetails();
        web = new WebView(this);
        WebSettings settings = web.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setSafeBrowsingEnabled(true);
        settings.setJavaScriptCanOpenWindowsAutomatically(false);
        settings.setSupportMultipleWindows(false);
        settings.setMediaPlaybackRequiresUserGesture(true);
        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, false);
        web.setWebViewClient(new WebViewClient() {
            @Override public void onPageStarted(WebView view, String url, android.graphics.Bitmap favicon) {
                pageLoadFailed = false;
                pageState = R.string.connection_loading;
                browserStatus.setText(policy.host);
                updateConnectionDetails();
            }

            @Override public void onPageFinished(WebView view, String url) {
                if (!pageLoadFailed) pageState = R.string.connection_page_ready;
                updateConnectionDetails();
            }

            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                String address = request.getUrl().toString();
                if (policy.matches(address)) return false;
                if (request.isForMainFrame() && request.hasGesture() && OriginPolicy.isExternalWebURL(address)) {
                    new AlertDialog.Builder(MainActivity.this).setMessage(R.string.open_external)
                            .setPositiveButton(R.string.open, (dialog, which) -> openBrowser(address))
                            .setNegativeButton(R.string.cancel, null).show();
                }
                return true;
            }

            @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                if (policy.allowsResource(request.getUrl().toString())) return null;
                return new WebResourceResponse("text/plain", "UTF-8", 403, "Blocked origin", null,
                        new ByteArrayInputStream("Blocked origin".getBytes(StandardCharsets.UTF_8)));
            }

            @Override public void onReceivedHttpAuthRequest(WebView view, HttpAuthHandler handler, String host, String challengeRealm) {
                // The random proxy credential must never be supplied to the remote website.
                if ("127.0.0.1".equals(host) && realm.equals(challengeRealm)) handler.proceed(username, password);
                else handler.cancel();
            }

            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                handler.cancel();
                pageLoadFailed = true;
                pageState = R.string.connection_failed;
                browserStatus.setText(R.string.tls_failed);
                updateConnectionDetails();
            }

            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) {
                    pageLoadFailed = true;
                    pageState = R.string.connection_failed;
                    browserStatus.setText(getString(R.string.web_failed, error.getDescription()));
                    updateConnectionDetails();
                }
            }

            @Override public void onReceivedHttpError(WebView view, WebResourceRequest request, WebResourceResponse response) {
                if (request.isForMainFrame()) {
                    pageLoadFailed = true;
                    pageState = R.string.connection_failed;
                    browserStatus.setText(getString(R.string.web_http_failed, response.getStatusCode()));
                    updateConnectionDetails();
                }
            }

            @Override public boolean onRenderProcessGone(WebView view, RenderProcessGoneDetail detail) {
                if (view != web) return true;
                ViewGroup parent = (ViewGroup) view.getParent();
                if (parent != null) parent.removeView(view);
                web = null;
                view.destroy();
                disconnect();
                status.setText(R.string.web_renderer_gone);
                return true;
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            @Override public void onPermissionRequest(PermissionRequest request) { request.deny(); }

            @Override public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback, FileChooserParams params) {
                if (pendingFiles != null) pendingFiles.onReceiveValue(null);
                pendingFiles = callback;
                Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*");
                intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, params.getMode() == FileChooserParams.MODE_OPEN_MULTIPLE);
                try {
                    startActivityForResult(Intent.createChooser(intent, getString(R.string.files)), FILE_REQUEST);
                } catch (ActivityNotFoundException e) {
                    pendingFiles.onReceiveValue(null);
                    pendingFiles = null;
                }
                return true;
            }
        });
        web.setDownloadListener((url, userAgent, disposition, mime, length) ->
                Toast.makeText(this, R.string.download_unavailable, Toast.LENGTH_LONG).show());
        browserPage.addView(web, new LinearLayout.LayoutParams(-1, 0, 1));
        root.addView(browserPage, new FrameLayout.LayoutParams(-1, -1));
        connectionPage.setVisibility(View.GONE);
        ProxyConfig config = new ProxyConfig.Builder().addProxyRule("http://" + proxyAddress).removeImplicitRules().build();
        // No direct fallback: losing the tsnet proxy must fail closed.
        if (WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)) {
            proxyConfigured = true;
            ProxyController.getInstance().setProxyOverride(config, this::runOnUiThread, () -> {
                if (generation == token && !destroyed && web != null) web.loadUrl(policy.origin + "/");
            });
        } else {
            disconnect();
            status.setText(R.string.proxy_unsupported);
        }
    }

    private void fail(Exception e) {
        if (httpsState == R.string.connection_checking) httpsState = R.string.connection_failed;
        updateConnectionDetails();
        status.setText(getString(R.string.failed, safeMessage(e)));
        connectButton.setEnabled(true);
        connectButton.setText(R.string.retry);
        serverInput.setEnabled(true);
    }

    private void updateConnectionDetails() {
        String route = getString(R.string.connection_path_waiting);
        String latency = getString(R.string.connection_unknown_value);
        String target = getString(R.string.connection_unknown_value);
        String sampled = getString(R.string.connection_unknown_value);
        if (diagnosticsUnavailable) route = getString(R.string.connection_unavailable);
        if (connectionInfo != null) {
            switch (connectionInfo.optString("path")) {
                case "direct": route = getString(R.string.connection_direct); break;
                case "derp":
                    String relay = connectionInfo.optString("relay");
                    route = getString(R.string.connection_derp) + (relay.isEmpty() ? "" : " (" + relay + ")");
                    break;
                case "peer-relay": route = getString(R.string.connection_peer_relay); break;
                default:
                    switch (connectionInfo.optString("reason")) {
                        case "target_unknown": route = getString(R.string.connection_target_unknown); break;
                        case "probe_timeout": route = getString(R.string.connection_probe_timeout); break;
                        case "probe_failed": route = getString(R.string.connection_probe_failed); break;
                        case "status_unavailable": route = getString(R.string.connection_unavailable); break;
                        case "not_running": route = getString(R.string.connection_path_waiting); break;
                        default: route = getString(R.string.connection_path_unknown);
                    }
            }
            if (connectionInfo.has("latencyMs") && !connectionInfo.isNull("latencyMs")) {
                latency = String.format(Locale.getDefault(), "%.1f ms", connectionInfo.optDouble("latencyMs"));
            }
            target = connectionInfo.optString("targetIP", target);
            long timestamp = connectionInfo.optLong("sampledAtMs");
            if (timestamp > 0) sampled = DateFormat.getTimeInstance(DateFormat.MEDIUM).format(new Date(timestamp));
        }
        String state;
        switch (tailnetState) {
            case "Running": state = getString(R.string.connection_tailnet_ready); break;
            case "NeedsLogin": state = getString(R.string.connection_needs_login); break;
            case "NeedsMachineAuth": state = getString(R.string.connection_needs_approval); break;
            case "Starting": case "NoState": state = getString(R.string.connection_starting); break;
            case "Disconnected": case "Stopped": state = getString(R.string.connection_disconnected); break;
            case "Unknown": case "": state = getString(R.string.connection_path_unknown); break;
            default: state = tailnetState;
        }
        String text = String.join("\n", getString(R.string.connection_tailnet_row, state),
                getString(R.string.connection_path_row, route), getString(R.string.connection_latency_row, latency),
                getString(R.string.connection_target_row, target), getString(R.string.connection_https_row, getString(httpsState)),
                getString(R.string.connection_page_row, getString(pageState)), getString(R.string.connection_updated_row, sampled));
        if (!text.contentEquals(connectionDetails.getText())) connectionDetails.setText(text);
        if (browserConnectionDetails != null) {
            String compact = getString(R.string.connection_compact, route, latency);
            if (!compact.contentEquals(browserConnectionDetails.getText())) browserConnectionDetails.setText(compact);
        }
        if (connectionDialog != null) connectionDialog.setMessage(text + "\n\n" + getString(R.string.connection_note));
    }

    private static String safeMessage(Exception e) {
        String text = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
        return text.length() > 500 ? text.substring(0, 500) : text;
    }

    private void post(int token, Runnable action) {
        main.post(() -> { if (!destroyed && generation == token) action.run(); });
    }

    private void openBrowser(String address) {
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(address)).addCategory(Intent.CATEGORY_BROWSABLE));
        } catch (ActivityNotFoundException e) {
            Toast.makeText(this, R.string.no_browser, Toast.LENGTH_LONG).show();
        }
    }

    private void destroyWeb() {
        if (connectionDialog != null) connectionDialog.dismiss();
        browserConnectionDetails = null;
        if (pendingFiles != null) { pendingFiles.onReceiveValue(null); pendingFiles = null; }
        if (web != null) {
            web.stopLoading();
            web.setWebChromeClient(null);
            ((ViewGroup) web.getParent()).removeView(web);
            web.destroy();
            web = null;
        }
        if (browserPage != null) { root.removeView(browserPage); browserPage = null; }
        if (proxyConfigured) {
            if (WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)) {
                ProxyController.getInstance().clearProxyOverride(this::runOnUiThread, () -> {});
            }
            proxyConfigured = false;
        }
    }

    private void disconnect() {
        ++generation;
        connectionMonitor.stop();
        destroyWeb();
        worker.execute(this::closeClient);
        loginURL = "";
        connectionPage.setVisibility(View.VISIBLE);
        loginButton.setVisibility(View.GONE);
        cancelButton.setVisibility(View.GONE);
        serverInput.setEnabled(true);
        connectButton.setEnabled(true);
        connectButton.setText(R.string.connect);
        status.setText(R.string.ready);
        connectionInfo = null;
        diagnosticsUnavailable = false;
        tailnetState = "Disconnected";
        httpsState = R.string.connection_not_checked;
        pageState = R.string.connection_not_open;
        updateConnectionDetails();
    }

    private void closeClient() {
        if (client == null) return;
        try { client.close(); } catch (Exception ignored) { /* State is retained for the next attempt. */ }
        client = null;
    }

    private void handleBack() {
        if (web != null && web.canGoBack()) web.goBack();
        else if (web != null || !connectButton.isEnabled()) disconnect();
        else finish();
    }

    // Android 13+ uses the OnBackInvokedDispatcher callback registered in onCreate.
    @SuppressLint("GestureBackNavigation")
    @Override public void onBackPressed() { handleBack(); }

    @Override protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != FILE_REQUEST || pendingFiles == null) return;
        ArrayList<Uri> uris = new ArrayList<>();
        if (resultCode == RESULT_OK && data != null) {
            ClipData clips = data.getClipData();
            if (clips != null) {
                for (int i = 0; i < clips.getItemCount() && i < 32; i++) {
                    Uri uri = clips.getItemAt(i).getUri();
                    if ("content".equals(uri.getScheme())) uris.add(uri);
                }
            } else if (data.getData() != null && "content".equals(data.getData().getScheme())) {
                uris.add(data.getData());
            }
        }
        pendingFiles.onReceiveValue(uris.isEmpty() ? null : uris.toArray(new Uri[0]));
        pendingFiles = null;
    }

    @Override protected void onStart() {
        super.onStart();
        connectionMonitor.setActive(true);
    }

    @Override protected void onStop() {
        connectionMonitor.setActive(false);
        super.onStop();
    }

    @Override protected void onDestroy() {
        destroyed = true;
        ++generation;
        connectionMonitor.stop();
        diagnosticWorker.shutdown();
        main.removeCallbacksAndMessages(null);
        if (networkCallback != null) connectivity.unregisterNetworkCallback(networkCallback);
        destroyWeb();
        worker.execute(this::closeClient);
        worker.shutdown();
        super.onDestroy();
    }
}
