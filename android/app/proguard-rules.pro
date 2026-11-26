# JNI exports in libspool-textmate resolve these names directly.
-keep class com.cloudgeek.spool.textmate.NativeOniguruma {
    private static native <methods>;
}

# The gomobile AAR supplies its own keep rules for go.** and spoolnet.**.

# The offline Mermaid document exports bounded, self-contained SVG for native display.
-keepclassmembers class com.cloudgeek.spool.android.NativeMermaid$Bridge {
    @android.webkit.JavascriptInterface <methods>;
}
