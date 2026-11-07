package i18n

// #cgo LDFLAGS: -framework CoreFoundation
// #include <CoreFoundation/CoreFoundation.h>
// static int preferredLanguage(long index, char *buffer, int size) {
//   CFArrayRef languages = CFLocaleCopyPreferredLanguages();
//   int found = index < CFArrayGetCount(languages) && CFStringGetCString(CFArrayGetValueAtIndex(languages, index), buffer, size, kCFStringEncodingUTF8);
//   CFRelease(languages);
//   return found;
// }
import "C"

// SystemLocale 按 macOS 用户首选的语言返回最接近的支持语言，都不支持时为英文
func SystemLocale() string {
	var buffer [64]C.char
	for index := C.long(0); C.preferredLanguage(index, &buffer[0], C.int(len(buffer))) != 0; index++ {
		if locale := Match(C.GoString(&buffer[0])); locale != "" {
			return locale
		}
	}
	return "en"
}
