# JNI exports in libspool-textmate resolve these names directly.
-keep class com.cloudgeek.spool.textmate.NativeOniguruma {
    private static native <methods>;
}

# The gomobile AAR supplies its own keep rules for go.** and spoolnet.**.
