// Included only when compiling the lifecycle test's disposable helper. Stop at
// the actual spawn boundary, before the app can send its child PID to Spool.
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static int startupFaultSpawn(pid_t *pid, const char *path,
                             const posix_spawn_file_actions_t *files,
                             const posix_spawnattr_t *attributes,
                             char *const argv[], char *const envp[]) {
    int error = posix_spawn(pid, path, files, attributes, argv, envp);
    const char *mode = getenv("SPOOL_TEST_STARTUP_FAULT");
    const char *record = getenv("SPOOL_TEST_STARTUP_PIDS");
    if (error != 0 || !mode || !record) return error;
    FILE *output = fopen(record, "w");
    if (!output) _exit(79);
    fprintf(output, "{\"helper_pid\":%d,\"voxl_pid\":%d}\n", getpid(), *pid);
    fclose(output);
    if (strcmp(mode, "exit") == 0) _exit(78);
    if (strcmp(mode, "wait") == 0) for (;;) pause();
    return error;
}

#define posix_spawn startupFaultSpawn
