#include <jni.h>
#include <stdint.h>
#include <stdlib.h>
#include <oniguruma.h>

static void fail(JNIEnv *env, const char *type, const char *message) {
    jclass error = (*env)->FindClass(env, type);
    if (error != NULL) (*env)->ThrowNew(env, error, message);
}

JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM *vm, void *reserved) {
    OnigEncoding encodings[] = {ONIG_ENCODING_UTF8};
    return onig_initialize(encodings, 1) == ONIG_NORMAL ? JNI_VERSION_1_6 : JNI_ERR;
}

JNIEXPORT jlong JNICALL Java_com_cloudgeek_spool_textmate_NativeOniguruma_nCompile(JNIEnv *env, jclass type, jbyteArray pattern, jboolean ignore_case) {
    jsize length = (*env)->GetArrayLength(env, pattern);
    jbyte *bytes = (*env)->GetByteArrayElements(env, pattern, NULL);
    if (bytes == NULL) return 0;
    OnigRegex regex = NULL;
    OnigErrorInfo error;
    int options = ONIG_OPTION_CAPTURE_GROUP | (ignore_case ? ONIG_OPTION_IGNORECASE : 0);
    int status = onig_new(&regex, (OnigUChar *) bytes, (OnigUChar *) bytes + length, options, ONIG_ENCODING_UTF8, ONIG_SYNTAX_DEFAULT, &error);
    if (status != ONIG_NORMAL) {
        OnigUChar message[ONIG_MAX_ERROR_MESSAGE_LEN];
        onig_error_code_to_str(message, status, &error);
        fail(env, "java/lang/IllegalArgumentException", (char *) message);
    }
    (*env)->ReleaseByteArrayElements(env, pattern, bytes, JNI_ABORT);
    return (jlong) (intptr_t) regex;
}

JNIEXPORT jintArray JNICALL Java_com_cloudgeek_spool_textmate_NativeOniguruma_nSearch(JNIEnv *env, jclass type, jlong pointer, jbyteArray text, jint start) {
    jsize length = (*env)->GetArrayLength(env, text);
    jbyte *bytes = (*env)->GetByteArrayElements(env, text, NULL);
    if (bytes == NULL) return NULL;
    OnigRegion *region = onig_region_new();
    OnigMatchParam *parameters = onig_new_match_param();
    if (region == NULL || parameters == NULL) {
        (*env)->ReleaseByteArrayElements(env, text, bytes, JNI_ABORT);
        if (region != NULL) onig_region_free(region, 1);
        if (parameters != NULL) onig_free_match_param(parameters);
        fail(env, "java/lang/OutOfMemoryError", "Allocating native regex match");
        return NULL;
    }
    // A pathological file cannot hold the shared syntax worker indefinitely.
    onig_set_retry_limit_in_match_of_match_param(parameters, 1000000);
    onig_set_retry_limit_in_search_of_match_param(parameters, 1000000);
    int status = onig_search_with_param((OnigRegex) (intptr_t) pointer, (OnigUChar *) bytes, (OnigUChar *) bytes + length,
            (OnigUChar *) bytes + start, (OnigUChar *) bytes + length, region, ONIG_OPTION_DEFAULT, parameters);
    (*env)->ReleaseByteArrayElements(env, text, bytes, JNI_ABORT);
    jintArray result = NULL;
    if (status >= 0) {
        jint *captures = malloc(sizeof(jint) * region->num_regs * 2);
        if (captures == NULL) fail(env, "java/lang/OutOfMemoryError", "Allocating native regex captures");
        else {
            for (int i = 0; i < region->num_regs; i++) { captures[i * 2] = region->beg[i]; captures[i * 2 + 1] = region->end[i]; }
            result = (*env)->NewIntArray(env, region->num_regs * 2);
            if (result != NULL) (*env)->SetIntArrayRegion(env, result, 0, region->num_regs * 2, captures);
            free(captures);
        }
    } else if (status != ONIG_MISMATCH) {
        fail(env, "java/lang/IllegalStateException", "Native syntax regex exceeded its matching limit");
    }
    onig_region_free(region, 1);
    onig_free_match_param(parameters);
    return result;
}

JNIEXPORT void JNICALL Java_com_cloudgeek_spool_textmate_NativeOniguruma_nClose(JNIEnv *env, jclass type, jlong pointer) {
    if (pointer != 0) onig_free((OnigRegex) (intptr_t) pointer);
}
