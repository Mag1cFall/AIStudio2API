#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <objbase.h>
#include <oleauto.h>
#include <wincrypt.h>

#define ABE_INPUT_ENV L"AISTUDIO2API_ABE_INPUT"
#define ABE_OUTPUT_ENV L"AISTUDIO2API_ABE_OUTPUT"

typedef HRESULT(STDMETHODCALLTYPE *DecryptDataFn)(IUnknown *, BSTR, BSTR *, DWORD *);

static HMODULE g_module;

// read_env 读取宽字符环境变量
static wchar_t *read_env(const wchar_t *name) {
    DWORD size = GetEnvironmentVariableW(name, NULL, 0);
    if (size == 0) {
        return NULL;
    }
    wchar_t *value = (wchar_t *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, size * sizeof(wchar_t));
    if (value == NULL) {
        return NULL;
    }
    if (GetEnvironmentVariableW(name, value, size) == 0) {
        HeapFree(GetProcessHeap(), 0, value);
        return NULL;
    }
    return value;
}

// decode_base64 解码环境变量中的密文
static BOOL decode_base64(const wchar_t *input, BYTE **output, DWORD *output_size) {
    DWORD size = 0;
    if (!CryptStringToBinaryW(input, 0, CRYPT_STRING_BASE64, NULL, &size, NULL, NULL) || size == 0) {
        return FALSE;
    }
    BYTE *buffer = (BYTE *)HeapAlloc(GetProcessHeap(), 0, size);
    if (buffer == NULL) {
        return FALSE;
    }
    if (!CryptStringToBinaryW(input, 0, CRYPT_STRING_BASE64, buffer, &size, NULL, NULL)) {
        HeapFree(GetProcessHeap(), 0, buffer);
        return FALSE;
    }
    *output = buffer;
    *output_size = size;
    return TRUE;
}
// write_output 写出成功结果或错误信息
static void write_output(const wchar_t *path, const BYTE *data, DWORD size) {
    HANDLE file = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
    if (file == INVALID_HANDLE_VALUE) {
        return;
    }
    DWORD written = 0;
    WriteFile(file, data, size, &written, NULL);
    CloseHandle(file);
}

// write_error 写出短错误码
static void write_error(const wchar_t *path, const char *message) {
    write_output(path, (const BYTE *)message, (DWORD)lstrlenA(message));
}

// append_str 追加 ASCII 字符串，返回新的写入位置
static char *append_str(char *dst, char *end, const char *src) {
    while (*src != '\0' && dst < end) {
        *dst++ = *src++;
    }
    return dst;
}

// append_hex32 追加 8 位小写十六进制数
static char *append_hex32(char *dst, char *end, DWORD value) {
    static const char digits[] = "0123456789abcdef";
    for (int shift = 28; shift >= 0 && dst < end; shift -= 4) {
        *dst++ = digits[(value >> shift) & 0xF];
    }
    return dst;
}

// append_dec 追加无符号十进制数
static char *append_dec(char *dst, char *end, DWORD value) {
    char buf[16];
    int n = 0;
    do {
        buf[n++] = (char)('0' + (value % 10));
        value /= 10;
    } while (value != 0);
    while (n > 0 && dst < end) {
        *dst++ = buf[--n];
    }
    return dst;
}

// write_decrypt_error 写出带阶段、HRESULT 与系统错误码的诊断信息
static void write_decrypt_error(const wchar_t *path, const char *stage, HRESULT hr, DWORD last_error) {
    char buf[96];
    char *p = buf;
    char *end = buf + sizeof(buf) - 1;
    p = append_str(p, end, "ERR ");
    p = append_str(p, end, stage);
    p = append_str(p, end, " hr=0x");
    p = append_hex32(p, end, (DWORD)hr);
    p = append_str(p, end, " err=");
    p = append_dec(p, end, last_error);
    *p = '\0';
    write_error(path, buf);
}

// call_decrypt 调用 Chrome IElevator 解开 App-Bound 主密钥
static HRESULT call_decrypt(
    const BYTE *ciphertext,
    DWORD ciphertext_size,
    BYTE *plaintext,
    DWORD plaintext_size,
    const char **stage_out,
    DWORD *last_error_out
) {
    const IID clsid_chrome = {0x708860E0, 0xF641, 0x4611, {0x88, 0x95, 0x7D, 0x86, 0x7D, 0xD3, 0x67, 0x5B}};
    const IID iid_chrome_v2 = {0x1BF5208B, 0x295F, 0x4992, {0xB5, 0xF4, 0x3A, 0x9B, 0xB6, 0x49, 0x48, 0x38}};
    const IID iid_chrome_v1 = {0x463ABECF, 0x410D, 0x407F, {0x8A, 0xF5, 0x0D, 0xF3, 0x5A, 0x00, 0x5C, 0xC8}};
    *stage_out = "cocreate";
    *last_error_out = 0;
    IUnknown *object = NULL;
    HRESULT hr = CoCreateInstance(&clsid_chrome, NULL, CLSCTX_LOCAL_SERVER, &iid_chrome_v2, (void **)&object);
    if (FAILED(hr)) {
        hr = CoCreateInstance(&clsid_chrome, NULL, CLSCTX_LOCAL_SERVER, &iid_chrome_v1, (void **)&object);
    }
    if (FAILED(hr) || object == NULL) {
        return hr;
    }

    *stage_out = "blanket";
    hr = CoSetProxyBlanket(object, RPC_C_AUTHN_DEFAULT, RPC_C_AUTHZ_DEFAULT, NULL,
        RPC_C_AUTHN_LEVEL_PKT_PRIVACY, RPC_C_IMP_LEVEL_IMPERSONATE, NULL, EOAC_DYNAMIC_CLOAKING);
    if (FAILED(hr)) {
        object->lpVtbl->Release(object);
        return hr;
    }

    *stage_out = "alloc";
    BSTR input = SysAllocStringByteLen((LPCSTR)ciphertext, ciphertext_size);
    if (input == NULL) {
        object->lpVtbl->Release(object);
        return E_OUTOFMEMORY;
    }
    BSTR output = NULL;
    DWORD last_error = 0;
    void **vtable = *(void ***)object;
    DecryptDataFn decrypt_data = (DecryptDataFn)vtable[5];
    *stage_out = "decrypt";
    hr = decrypt_data(object, input, &output, &last_error);
    SysFreeString(input);
    if (FAILED(hr)) {
        *last_error_out = last_error;
        object->lpVtbl->Release(object);
        return hr;
    }
    *stage_out = "size";
    UINT size = SysStringByteLen(output);
    if (size != plaintext_size) {
        SysFreeString(output);
        object->lpVtbl->Release(object);
        return E_FAIL;
    }
    CopyMemory(plaintext, (const BYTE *)output, plaintext_size);
    SysFreeString(output);
    object->lpVtbl->Release(object);
    return S_OK;
}

// worker 解密后通知 Go 父进程
static DWORD WINAPI worker(LPVOID parameter) {
    (void)parameter;
    wchar_t *input_env = read_env(ABE_INPUT_ENV);
    wchar_t *output_path = read_env(ABE_OUTPUT_ENV);
    if (input_env == NULL || output_path == NULL) {
        goto done;
    }
    BYTE *ciphertext = NULL;
    DWORD ciphertext_size = 0;
    if (!decode_base64(input_env, &ciphertext, &ciphertext_size)) {
        write_error(output_path, "ERR base64");
        goto done;
    }
    HRESULT hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    BOOL com_initialized = SUCCEEDED(hr);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) {
        write_decrypt_error(output_path, "cominit", hr, 0);
        HeapFree(GetProcessHeap(), 0, ciphertext);
        goto done;
    }
    BYTE key[32] = {0};
    const char *stage = "decrypt";
    DWORD last_error = 0;
    hr = call_decrypt(ciphertext, ciphertext_size, key, sizeof(key), &stage, &last_error);
    if (SUCCEEDED(hr)) {
        write_output(output_path, key, sizeof(key));
    } else {
        write_decrypt_error(output_path, stage, hr, last_error);
    }
    if (com_initialized) {
        CoUninitialize();
    }
    HeapFree(GetProcessHeap(), 0, ciphertext);

done:
    if (input_env != NULL) {
        HeapFree(GetProcessHeap(), 0, input_env);
    }
    if (output_path != NULL) {
        HeapFree(GetProcessHeap(), 0, output_path);
    }
    FreeLibraryAndExitThread(g_module, 0);
    return 0;
}

// DllMain 启动独立工作线程
BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID reserved) {
    (void)reserved;
    if (reason == DLL_PROCESS_ATTACH) {
        g_module = instance;
        DisableThreadLibraryCalls(instance);
        HANDLE thread = CreateThread(NULL, 0, worker, NULL, 0, NULL);
        if (thread != NULL) {
            CloseHandle(thread);
        }
    }
    return TRUE;
}
