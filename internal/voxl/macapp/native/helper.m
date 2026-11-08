// Built locally once, independently of Spool releases. The --launch client is
// owned by Spool. LaunchServices starts --run as the independent TCC application.
// A private authenticated loopback connection is both control channel and lease.
#import <AppKit/AppKit.h>
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <libproc.h>
#include <poll.h>
#include <signal.h>
#include <spawn.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

// The main thread, signal handlers and worker all share this lock-free flag.
static atomic_bool stopping = false;
static void stopSignal(int sig) { (void)sig; atomic_store_explicit(&stopping, true, memory_order_relaxed); }

static double now(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec + t.tv_nsec / 1e9;
}

static void pump(void) {
    [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
}

static bool leaseAlive(void) {
    if (stopping) return false;
    struct pollfd p = { .fd = STDIN_FILENO, .events = POLLIN };
    // The pipe carries no data. Readability, hangup, and errors all revoke it.
    return poll(&p, 1, 0) <= 0;
}

static int controlSocket(void) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd >= 0) {
        fcntl(fd, F_SETFD, FD_CLOEXEC);
        int yes = 1;
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof(yes));
    }
    return fd;
}

static bool sendLine(int fd, const char *text) {
    size_t len = strlen(text);
    while (len) {
        ssize_t n = send(fd, text, len, MSG_DONTWAIT);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) return false;
        text += n;
        len -= (size_t)n;
    }
    return true;
}

static bool readLine(int fd, char *line, size_t capacity, double deadline, bool checkLease) {
    size_t used = 0;
    while (now() < deadline && used + 1 < capacity) {
        if (checkLease && !leaseAlive()) return false;
        struct pollfd p = { .fd = fd, .events = POLLIN };
        int result = poll(&p, 1, 50);
        if (result < 0 && errno == EINTR) continue;
        if (result < 0) return false;
        if (result == 0) continue;
        char c;
        if (read(fd, &c, 1) != 1) return false;
        if (c == '\n') { line[used] = 0; return true; }
        line[used++] = c;
    }
    return false;
}

static bool authenticate(const char *line, const char *token) {
    if (strlen(line) != 64) return false;
    unsigned difference = 0;
    for (size_t i = 0; i < 64; ++i) difference |= (unsigned char)line[i] ^ (unsigned char)token[i];
    return difference == 0;
}

// Keep the child's PID reserved until its entire process group is stopped.
// WNOWAIT avoids reusing a reaped PID while cleaning up descendants.
static bool childExited(pid_t pid) {
    siginfo_t info = {0};
    return waitid(P_PID, (id_t)pid, &info, WEXITED | WNOHANG | WNOWAIT) == 0 && info.si_pid == pid;
}

static int reapChild(pid_t pid, bool graceful) {
    if (graceful) {
        kill(-pid, SIGTERM);
        double deadline = now() + 2;
        while (!childExited(pid) && now() < deadline) usleep(20000);
    }
    kill(-pid, SIGKILL);
    int status = 0;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno != EINTR) return 1;
    }
    if (WIFEXITED(status)) return WEXITSTATUS(status);
    return 128 + (WIFSIGNALED(status) ? WTERMSIG(status) : 0);
}

typedef struct { pid_t pid; uint64_t seconds; uint64_t microseconds; } ProcessIdentity;

static ProcessIdentity identity(pid_t pid) {
    struct proc_bsdinfo info = {0};
    ProcessIdentity result = {0};
    if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) == sizeof(info)) {
        result.pid = pid;
        result.seconds = info.pbi_start_tvsec;
        result.microseconds = info.pbi_start_tvusec;
    }
    return result;
}

static bool sameProcess(ProcessIdentity process) {
    if (process.pid <= 0) return false;
    ProcessIdentity current = identity(process.pid);
    return current.pid == process.pid && current.seconds == process.seconds && current.microseconds == process.microseconds;
}

static void killOwnedChild(ProcessIdentity child) {
    if (sameProcess(child) && getpgid(child.pid) == child.pid) {
        kill(-child.pid, SIGKILL);
    }
}

// This process keeps the same PID and process group when it execs voxl. Until
// the launcher owns that identity, only the helper holds the pipe's write end.
// A helper crash before its PID acknowledgement therefore exits this gate
// without ever starting capture.
static int runEngine(void) {
    char line[16];
    if (!readLine(STDIN_FILENO, line, sizeof(line), now() + 8, false) || strcmp(line, "EXEC") != 0 || stopping) return 1;
    int input = open("/dev/null", O_RDONLY);
    if (input < 0) return 1;
    if (dup2(input, STDIN_FILENO) < 0) { close(input); return 1; }
    if (input != STDIN_FILENO) close(input);
    NSString *executable = [NSBundle.mainBundle.executablePath.stringByDeletingLastPathComponent stringByAppendingPathComponent:@"voxl"];
    NSArray<NSString *> *arguments = NSProcessInfo.processInfo.arguments;
    char **argv = calloc(arguments.count, sizeof(char *));
    argv[0] = strdup(executable.fileSystemRepresentation);
    for (NSUInteger i = 2; i < arguments.count; ++i) argv[i - 1] = strdup(arguments[i].UTF8String);
    execv(executable.fileSystemRepresentation, argv);
    return 1;
}

static int runHelper(void) {
    NSDictionary<NSString *, NSString *> *env = NSProcessInfo.processInfo.environment;
    NSString *token = env[@"SPOOL_VOXL_CONTROL_TOKEN"];
    NSString *portText = env[@"SPOOL_VOXL_CONTROL_PORT"];
    NSString *directory = env[@"SPOOL_VOXL_DIRECTORY"];
    NSInteger port = portText.integerValue;
    if (token.length != 64 || port <= 0 || port > 65535 || !directory.isAbsolutePath) return 64;
    int fd = controlSocket();
    if (fd < 0) return 1;
    struct sockaddr_in address = { .sin_family = AF_INET, .sin_port = htons((uint16_t)port), .sin_addr.s_addr = htonl(INADDR_LOOPBACK) };
    char hello[80];
    snprintf(hello, sizeof(hello), "%s\n", token.UTF8String);
    char line[128];
    if (connect(fd, (struct sockaddr *)&address, sizeof(address)) != 0 || !sendLine(fd, hello) || !readLine(fd, line, sizeof(line), now() + 8, false) || strcmp(line, "START") != 0 || stopping) {
        close(fd);
        return 1;
    }
    if (chdir(directory.fileSystemRepresentation) != 0) { close(fd); return 1; }
    int gate[2];
    if (pipe(gate) != 0) { close(fd); return 1; }
    if (fcntl(gate[0], F_SETFD, FD_CLOEXEC) != 0 || fcntl(gate[1], F_SETFD, FD_CLOEXEC) != 0) {
        close(gate[0]); close(gate[1]); close(fd); return 1;
    }
    NSString *executable = NSBundle.mainBundle.executablePath;
    NSArray<NSString *> *arguments = NSProcessInfo.processInfo.arguments;
    char **argv = calloc(arguments.count + 1, sizeof(char *));
    argv[0] = strdup(executable.fileSystemRepresentation);
    argv[1] = strdup("--exec");
    for (NSUInteger i = 2; i < arguments.count; ++i) argv[i] = strdup(arguments[i].UTF8String);
    NSMutableDictionary<NSString *, NSString *> *childEnv = env.mutableCopy;
    for (NSString *key in @[@"SPOOL_VOXL_CONTROL_TOKEN", @"SPOOL_VOXL_CONTROL_PORT", @"SPOOL_VOXL_DIRECTORY"]) [childEnv removeObjectForKey:key];
    char **envp = calloc(childEnv.count + 1, sizeof(char *));
    NSUInteger index = 0;
    for (NSString *key in childEnv) envp[index++] = strdup([NSString stringWithFormat:@"%@=%@", key, childEnv[key]].UTF8String);
    posix_spawnattr_t attr;
    posix_spawnattr_init(&attr);
    posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETPGROUP);
    posix_spawnattr_setpgroup(&attr, 0);
    posix_spawn_file_actions_t files;
    posix_spawn_file_actions_init(&files);
    posix_spawn_file_actions_adddup2(&files, gate[0], STDIN_FILENO);
    if (gate[0] != STDIN_FILENO) posix_spawn_file_actions_addclose(&files, gate[0]);
    posix_spawn_file_actions_addclose(&files, gate[1]);
    posix_spawn_file_actions_addopen(&files, STDOUT_FILENO, "/dev/null", O_WRONLY, 0);
    posix_spawn_file_actions_addopen(&files, STDERR_FILENO, "/dev/null", O_WRONLY, 0);
    pid_t child = 0;
    int error = posix_spawn(&child, executable.fileSystemRepresentation, &files, &attr, argv, envp);
    close(gate[0]);
    posix_spawn_file_actions_destroy(&files);
    posix_spawnattr_destroy(&attr);
    for (NSUInteger i = 0; argv[i]; ++i) free(argv[i]);
    for (NSUInteger i = 0; envp[i]; ++i) free(envp[i]);
    free(argv);
    free(envp);
    if (error != 0) { close(gate[1]); close(fd); return 1; }
    snprintf(line, sizeof(line), "PREPARED %d\n", child);
    bool connected = sendLine(fd, line) && readLine(fd, line, sizeof(line), now() + 4, false) && strcmp(line, "CONTINUE") == 0 && !stopping;
    if (connected) connected = write(gate[1], "EXEC\n", 5) == 5;
    close(gate[1]);
    double heartbeat = now();
    bool naturalExit = false;
    while (connected && !stopping && now() - heartbeat < 5) {
        if (childExited(child)) { naturalExit = true; break; }
        struct pollfd p = { .fd = fd, .events = POLLIN };
        int ready = poll(&p, 1, 50);
        if (ready < 0 && errno == EINTR) continue;
        if (ready < 0) break;
        if (ready > 0) {
            connected = readLine(fd, line, sizeof(line), now() + 1, false) && strcmp(line, "PING") == 0;
            if (connected) heartbeat = now();
        }
    }
    int status = reapChild(child, !naturalExit);
    snprintf(line, sizeof(line), "EXIT %d\n", naturalExit ? status : 0);
    sendLine(fd, line);
    close(fd);
    return naturalExit ? status : 0;
}

@interface HelperDelegate : NSObject <NSApplicationDelegate>
@end
@implementation HelperDelegate
- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
        @autoreleasepool {
            int status = runHelper();
            dispatch_async(dispatch_get_main_queue(), ^{ exit(status); });
        }
    });
}
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    (void)sender;
    stopping = 1;
    return NSTerminateCancel; // The worker reaps voxl before exiting the app.
}
@end

static int launchHelper(void) {
    int server = controlSocket();
    if (server < 0) return 1;
    struct sockaddr_in address = { .sin_family = AF_INET, .sin_addr.s_addr = htonl(INADDR_LOOPBACK) };
    socklen_t length = sizeof(address);
    if (bind(server, (struct sockaddr *)&address, length) != 0 || listen(server, 8) != 0 || getsockname(server, (struct sockaddr *)&address, &length) != 0) {
        close(server); return 1;
    }
    unsigned char bytes[32];
    arc4random_buf(bytes, sizeof(bytes));
    char token[65];
    for (size_t i = 0; i < sizeof(bytes); ++i) snprintf(token + 2 * i, 3, "%02x", bytes[i]);
    NSWorkspaceOpenConfiguration *configuration = NSWorkspaceOpenConfiguration.configuration;
    configuration.activates = NO;
    configuration.addsToRecentItems = NO;
    configuration.promptsUserIfNeeded = NO;
    configuration.createsNewApplicationInstance = YES;
    configuration.allowsRunningApplicationSubstitution = NO;
    NSMutableArray *args = [NSMutableArray arrayWithObject:@"--run"];
    NSArray *original = NSProcessInfo.processInfo.arguments;
    if (original.count > 2) [args addObjectsFromArray:[original subarrayWithRange:NSMakeRange(2, original.count - 2)]];
    configuration.arguments = args;
    NSMutableDictionary *env = NSProcessInfo.processInfo.environment.mutableCopy;
    env[@"SPOOL_VOXL_CONTROL_TOKEN"] = @(token);
    env[@"SPOOL_VOXL_CONTROL_PORT"] = [NSString stringWithFormat:@"%u", ntohs(address.sin_port)];
    env[@"SPOOL_VOXL_DIRECTORY"] = NSFileManager.defaultManager.currentDirectoryPath;
    configuration.environment = env;
    __block NSRunningApplication *application = nil;
    __block BOOL completed = NO;
    [NSWorkspace.sharedWorkspace openApplicationAtURL:NSBundle.mainBundle.bundleURL configuration:configuration completionHandler:^(NSRunningApplication *app, NSError *error) {
        (void)error;
        dispatch_async(dispatch_get_main_queue(), ^{ application = app; completed = YES; });
    }];
    double deadline = now() + 8;
    while (!completed && now() < deadline) pump();
    if (!application) { close(server); return 1; }
    ProcessIdentity appIdentity = identity(application.processIdentifier);
    int fd = -1;
    deadline = now() + 8;
    while (leaseAlive() && !application.terminated && now() < deadline) {
        pump();
        struct pollfd p = { .fd = server, .events = POLLIN };
        if (poll(&p, 1, 20) <= 0) continue;
        int candidate = accept(server, NULL, NULL);
        if (candidate < 0) continue;
        fcntl(candidate, F_SETFD, FD_CLOEXEC);
        char line[128];
        if (readLine(candidate, line, sizeof(line), now() + 0.5, true) && authenticate(line, token)) { fd = candidate; break; }
        close(candidate);
    }
    close(server);
    char line[128];
    pid_t childPID = 0;
    ProcessIdentity child = {0};
    bool started = fd >= 0 && leaseAlive() && sendLine(fd, "START\n") && readLine(fd, line, sizeof(line), now() + 4, true) && sscanf(line, "PREPARED %d", &childPID) == 1 && childPID > 0;
    if (started) {
        child = identity(childPID);
        started = child.pid > 0 && getpgid(child.pid) == child.pid && leaseAlive() && sendLine(fd, "CONTINUE\n");
    }
    if (started) {
        // Non-sensitive process identifiers are useful for lifecycle diagnostics.
        printf("{\"helper_pid\":%d,\"voxl_pid\":%d}\n", application.processIdentifier, childPID);
        fflush(stdout);
    }
    double heartbeat = 0;
    int status = 1;
    bool exited = false;
    while (started && leaseAlive() && !application.terminated) {
        pump();
        if (now() - heartbeat >= 1) {
            if (!sendLine(fd, "PING\n")) break;
            heartbeat = now();
        }
        struct pollfd p = { .fd = fd, .events = POLLIN };
        if (poll(&p, 1, 20) > 0) {
            exited = readLine(fd, line, sizeof(line), now() + 1, false) && sscanf(line, "EXIT %d", &status) == 1;
            break;
        }
    }
    if (fd >= 0) {
        // EOF revokes capture. Keep the read side open until voxl is reaped.
        shutdown(fd, SHUT_WR);
        if (started && !exited) exited = readLine(fd, line, sizeof(line), now() + 4, false) && sscanf(line, "EXIT %d", &status) == 1;
        close(fd);
    }
    if (!exited) killOwnedChild(child);
    deadline = now() + 2;
    while (!application.terminated && now() < deadline) pump();
    if (!application.terminated) {
        // AppKit's quit request can be refused by a stalled supervisor. Match
        // the launched process before forcing it to stop, without PID reuse.
        if (sameProcess(appIdentity)) kill(appIdentity.pid, SIGKILL);
        deadline = now() + 1;
        while (!application.terminated && now() < deadline) pump();
    }
    return exited && application.terminated ? status : 1;
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        signal(SIGPIPE, SIG_IGN);
        signal(SIGTERM, stopSignal);
        signal(SIGINT, stopSignal);
        if (argc < 2) return 64; // Opening the app manually never starts capture.
        if (strcmp(argv[1], "--launch") == 0) return launchHelper();
        if (strcmp(argv[1], "--exec") == 0) return runEngine();
        if (strcmp(argv[1], "--run") != 0) return 64;
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
        __attribute__((objc_precise_lifetime)) HelperDelegate *delegate = [HelperDelegate new];
        app.delegate = delegate;
        [app run];
        return 0;
    }
}
