#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void trim_trailing_whitespace(char *s) {
    size_t n = strlen(s);
    while (n > 0 && isspace((unsigned char)s[n - 1])) {
        s[n - 1] = '\0';
        n--;
    }
}

#ifdef _WIN32
#include <windows.h>

static void read_registry_string(const char *subkey, const char *value_name, char *out, size_t out_len) {
    HKEY key = NULL;
    DWORD type = REG_SZ;
    DWORD size = (DWORD)out_len;
    LONG result = RegOpenKeyExA(HKEY_LOCAL_MACHINE, subkey, 0, KEY_READ, &key);
    if (result != ERROR_SUCCESS) {
        return;
    }

    result = RegQueryValueExA(key, value_name, NULL, &type, (LPBYTE)out, &size);
    if (result == ERROR_SUCCESS && (type == REG_SZ || type == REG_EXPAND_SZ)) {
        out[out_len - 1] = '\0';
        trim_trailing_whitespace(out);
    }

    RegCloseKey(key);
}

static int read_registry_dword(const char *subkey, const char *value_name, DWORD *value) {
    HKEY key = NULL;
    DWORD type = REG_DWORD;
    DWORD size = sizeof(DWORD);

    LONG result = RegOpenKeyExA(HKEY_LOCAL_MACHINE, subkey, 0, KEY_READ, &key);
    if (result != ERROR_SUCCESS) {
        return 0;
    }

    result = RegQueryValueExA(key, value_name, NULL, &type, (LPBYTE)value, &size);
    RegCloseKey(key);
    return result == ERROR_SUCCESS && type == REG_DWORD;
}

static void collect_system_spec(char *cpu_model, size_t model_len, int *cpu_threads, double *cpu_max_freq_mhz,
                                double *memory_total_gb) {
    const char *cpu_key = "HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\0";

    read_registry_string(cpu_key, "ProcessorNameString", cpu_model, model_len);

    DWORD threads = GetActiveProcessorCount(ALL_PROCESSOR_GROUPS);
    if (threads == 0) {
        SYSTEM_INFO info;
        GetSystemInfo(&info);
        threads = info.dwNumberOfProcessors;
    }
    *cpu_threads = (int)threads;

    DWORD mhz = 0;
    if (read_registry_dword(cpu_key, "~MHz", &mhz)) {
        *cpu_max_freq_mhz = (double)mhz;
    }

    MEMORYSTATUSEX mem_stat;
    mem_stat.dwLength = sizeof(mem_stat);
    if (GlobalMemoryStatusEx(&mem_stat)) {
        *memory_total_gb = (double)mem_stat.ullTotalPhys / (1024.0 * 1024.0 * 1024.0);
    }
}

#elif defined(__APPLE__)
#include <sys/sysctl.h>
#include <unistd.h>

static int read_sysctl_string(const char *name, char *out, size_t out_len) {
    size_t len = out_len;
    if (sysctlbyname(name, out, &len, NULL, 0) != 0 || len == 0) {
        return 0;
    }
    out[out_len - 1] = '\0';
    trim_trailing_whitespace(out);
    return 1;
}

static int read_sysctl_i64(const char *name, long long *out) {
    long long value = 0;
    size_t len = sizeof(value);
    if (sysctlbyname(name, &value, &len, NULL, 0) != 0) {
        return 0;
    }
    *out = value;
    return 1;
}

static void collect_system_spec(char *cpu_model, size_t model_len, int *cpu_threads, double *cpu_max_freq_mhz,
                                double *memory_total_gb) {
    long long value = 0;

    if (read_sysctl_string("machdep.cpu.brand_string", cpu_model, model_len) == 0) {
        read_sysctl_string("hw.model", cpu_model, model_len);
    }

    if (read_sysctl_i64("hw.logicalcpu", &value) && value > 0) {
        *cpu_threads = (int)value;
    }

    if (read_sysctl_i64("hw.cpufrequency_max", &value) && value > 0) {
        *cpu_max_freq_mhz = (double)value / 1000000.0;
    }

    if (read_sysctl_i64("hw.memsize", &value) && value > 0) {
        *memory_total_gb = (double)value / (1024.0 * 1024.0 * 1024.0);
    }
}

#elif defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__) || defined(__DragonFly__)
#include <sys/sysctl.h>
#include <unistd.h>

static int read_sysctl_string(const char *name, char *out, size_t out_len) {
    size_t len = out_len;
    if (sysctlbyname(name, out, &len, NULL, 0) != 0 || len == 0) {
        return 0;
    }
    out[out_len - 1] = '\0';
    trim_trailing_whitespace(out);
    return 1;
}

static int read_sysctl_i64(const char *name, long long *out) {
    unsigned char raw[16] = {0};
    size_t len = sizeof(raw);
    if (sysctlbyname(name, raw, &len, NULL, 0) != 0 || len == 0 || len > sizeof(raw)) {
        return 0;
    }

    long long value = 0;
    memcpy(&value, raw, len);
    *out = value;
    return 1;
}

static void collect_system_spec(char *cpu_model, size_t model_len, int *cpu_threads, double *cpu_max_freq_mhz,
                                double *memory_total_gb) {
    long long value = 0;

    if (read_sysctl_string("hw.model", cpu_model, model_len) == 0) {
        read_sysctl_string("machdep.cpu_brand", cpu_model, model_len);
    }

    if (read_sysctl_i64("hw.ncpu", &value) && value > 0) {
        *cpu_threads = (int)value;
    }

    if (read_sysctl_i64("dev.cpu.0.freq", &value) && value > 0) {
        *cpu_max_freq_mhz = (double)value;
    } else if (read_sysctl_i64("hw.clockrate", &value) && value > 0) {
        *cpu_max_freq_mhz = (double)value;
    }

    if (read_sysctl_i64("hw.physmem64", &value) && value > 0) {
        *memory_total_gb = (double)value / (1024.0 * 1024.0 * 1024.0);
    } else if (read_sysctl_i64("hw.physmem", &value) && value > 0) {
        *memory_total_gb = (double)value / (1024.0 * 1024.0 * 1024.0);
    }
}

#elif defined(__linux__)
#include <unistd.h>

static void read_cpu_model_linux(char *out, size_t out_len) {
    FILE *fp = fopen("/proc/cpuinfo", "r");
    if (fp == NULL) {
        return;
    }

    char line[1024];
    while (fgets(line, sizeof(line), fp) != NULL) {
        if (strncmp(line, "model name", 10) == 0) {
            char *colon = strchr(line, ':');
            if (colon == NULL) {
                continue;
            }
            colon++;
            while (*colon != '\0' && isspace((unsigned char)*colon)) {
                colon++;
            }
            snprintf(out, out_len, "%s", colon);
            trim_trailing_whitespace(out);
            break;
        }
    }

    fclose(fp);
}

static double read_cpu_max_freq_linux(void) {
    FILE *fp = fopen("/sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq", "r");
    if (fp != NULL) {
        double kHz = 0.0;
        if (fscanf(fp, "%lf", &kHz) == 1 && kHz > 0.0) {
            fclose(fp);
            return kHz / 1000.0;
        }
        fclose(fp);
    }

    fp = fopen("/proc/cpuinfo", "r");
    if (fp == NULL) {
        return 0.0;
    }

    double max_mhz = 0.0;
    char line[1024];
    while (fgets(line, sizeof(line), fp) != NULL) {
        if (strncmp(line, "cpu MHz", 7) == 0) {
            char *colon = strchr(line, ':');
            if (colon == NULL) {
                continue;
            }
            double mhz = atof(colon + 1);
            if (mhz > max_mhz) {
                max_mhz = mhz;
            }
        }
    }

    fclose(fp);
    return max_mhz;
}

static double read_memory_total_gb_linux(void) {
    FILE *fp = fopen("/proc/meminfo", "r");
    if (fp == NULL) {
        return 0.0;
    }

    char line[1024];
    while (fgets(line, sizeof(line), fp) != NULL) {
        if (strncmp(line, "MemTotal:", 9) == 0) {
            unsigned long long kb = 0;
            if (sscanf(line, "MemTotal: %llu kB", &kb) == 1) {
                fclose(fp);
                return (double)kb / (1024.0 * 1024.0);
            }
        }
    }

    fclose(fp);
    return 0.0;
}

static void collect_system_spec(char *cpu_model, size_t model_len, int *cpu_threads, double *cpu_max_freq_mhz,
                                double *memory_total_gb) {
    read_cpu_model_linux(cpu_model, model_len);

    long threads = sysconf(_SC_NPROCESSORS_ONLN);
    if (threads > 0) {
        *cpu_threads = (int)threads;
    }

    *cpu_max_freq_mhz = read_cpu_max_freq_linux();
    *memory_total_gb = read_memory_total_gb_linux();
}

#else
#include <unistd.h>

static void collect_system_spec(char *cpu_model, size_t model_len, int *cpu_threads, double *cpu_max_freq_mhz,
                                double *memory_total_gb) {
    (void)cpu_model;
    (void)model_len;
    (void)cpu_max_freq_mhz;

    long threads = sysconf(_SC_NPROCESSORS_ONLN);
    if (threads > 0) {
        *cpu_threads = (int)threads;
    }

#ifdef _SC_PHYS_PAGES
    long pages = sysconf(_SC_PHYS_PAGES);
    long page_size = sysconf(_SC_PAGESIZE);
    if (pages > 0 && page_size > 0) {
        *memory_total_gb = ((double)pages * (double)page_size) / (1024.0 * 1024.0 * 1024.0);
    }
#endif
}
#endif

static void print_json_escaped(const char *s) {
    putchar('"');
    while (*s != '\0') {
        unsigned char c = (unsigned char)*s;
        switch (c) {
            case '\\':
                fputs("\\\\", stdout);
                break;
            case '"':
                fputs("\\\"", stdout);
                break;
            case '\b':
                fputs("\\b", stdout);
                break;
            case '\f':
                fputs("\\f", stdout);
                break;
            case '\n':
                fputs("\\n", stdout);
                break;
            case '\r':
                fputs("\\r", stdout);
                break;
            case '\t':
                fputs("\\t", stdout);
                break;
            default:
                if (c < 0x20) {
                    fprintf(stdout, "\\u%04x", c);
                } else {
                    putchar((char)c);
                }
                break;
        }
        s++;
    }
    putchar('"');
}

int main(int argc, char **argv) {
    const char *tool_name = "get_system_spec";

    if (argc != 2) {
        fprintf(stderr, "usage: systemspec-c %s\n", tool_name);
        return 1;
    }

    if (strcmp(argv[1], tool_name) != 0) {
        fprintf(stderr, "unknown tool: %s\n", argv[1]);
        return 1;
    }

    char cpu_model[512] = "Unknown";
    int cpu_threads = 0;
    double cpu_max_freq_mhz = 0.0;
    double memory_total_gb = 0.0;

    collect_system_spec(cpu_model, sizeof(cpu_model), &cpu_threads, &cpu_max_freq_mhz, &memory_total_gb);

    if (cpu_model[0] == '\0') {
        snprintf(cpu_model, sizeof(cpu_model), "%s", "Unknown");
    }
    if (cpu_threads <= 0) {
        cpu_threads = 1;
    }
    if (cpu_max_freq_mhz < 0.0) {
        cpu_max_freq_mhz = 0.0;
    }
    if (memory_total_gb < 0.0) {
        memory_total_gb = 0.0;
    }

    printf("{\n");
    printf("  \"cpu_model\": ");
    print_json_escaped(cpu_model);
    printf(",\n");
    printf("  \"cpu_threads\": %d,\n", cpu_threads);
    printf("  \"cpu_max_freq_mhz\": %.2f,\n", cpu_max_freq_mhz);
    printf("  \"memory_total_gb\": \"%.2f\"\n", memory_total_gb);
    printf("}\n");

    return 0;
}
