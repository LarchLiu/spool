package com.cloudgeek.spool.android;

import org.robolectric.config.AndroidConfigurer;
import org.robolectric.internal.bytecode.InstrumentationConfiguration;
import org.robolectric.internal.bytecode.Interceptors;
import org.robolectric.internal.bytecode.ShadowProviders;

/** Keep the VM's JNI library and all of its handle types in one classloader. */
public final class SpoolAndroidConfigurer extends AndroidConfigurer {
    public SpoolAndroidConfigurer(ShadowProviders providers) { super(providers); }

    @Override public void configure(InstrumentationConfiguration.Builder builder, Interceptors interceptors) {
        super.configure(builder, interceptors);
        // The bridge uses only Java types. Sharing it also lets plain JUnit and
        // API-specific Robolectric sandboxes use the same real native library.
        builder.doNotAcquireClass("com.cloudgeek.spool.textmate.NativeOniguruma");
        builder.doNotAcquirePackage("com.cloudgeek.spool.textmate.NativeOniguruma$");
    }
}
