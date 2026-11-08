# Only the isolated smoke variant can inject a synthetic chat and inspect its
# Activity fields. The production release does not consume these keep rules.
-keepclassmembers class com.cloudgeek.spool.android.MainActivity {
    boolean useMarkwonHistory;
    org.json.JSONObject user;
    org.json.JSONObject chat;
    java.lang.String chatID;
    com.cloudgeek.spool.android.NativeMarkwonHistory markwonHistory;
    com.cloudgeek.spool.android.NativeUi ui;
    androidx.core.widget.NestedScrollView history;
    void closeDrawer();
    void render();
}
