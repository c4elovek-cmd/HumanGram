// JNI-мост к HumanGram-Proxy для Android.
//
// Собирается только для Android (build tag android). На остальных
// платформах файл игнорируется.
//
// Зачем он нужен: при сборке в режиме c-shared Go экспортирует
// обычные C-символы (HumanGramProxyStart и так далее). Java же
// связывается с нативными методами по схеме JNI, ожидая символы с
// именами вида Java_org_telegram_messenger_HumanGramProxy_nativeStart.
// Без объявления таких обёрток вызов native-метода завершается
// ошибкой "No implementation found".
//
// Поэтому ниже объявлены функции с именами, требуемыми JNI, которые
// просто переадресуют вызовы в общий C API из bridge.go. Отдельные
// C-символы при этом продолжают работать: клиент на Windows и C++
// обращаются к ним напрямую.
//
// Лицензия: MIT. Copyright (c) 2026 HumanGram contributors.
//
// Данный файл — часть HumanGram Android, производного от Telegram
// Android, который распространяется по лицензии GPL-2.0.

//go:build android

package main

/*
#include <jni.h>
#include <stdlib.h>

// Общий C API, объявленный в bridge.go.
extern int    HumanGramProxyStart(const char *config);
extern char  *HumanGramProxyAddr(void);
extern int    HumanGramProxyPort(void);
extern int    HumanGramProxySessions(void);
extern char  *HumanGramProxyLastError(void);
extern void   HumanGramProxyFreeString(char *value);
extern int    HumanGramProxyStop(void);
extern int    HumanGramProxyIsRunning(void);
extern char  *HumanGramProxyVersion(void);

// Признак наличия JNI-моста. Используется только для того, чтобы
// cgo увидел ссылку на преамбулу.
static int hgJniBridgePresent(void) {
	return 1;
}

// Освобождает C-строку и возвращает новую jstring.
// При ошибке выделения памяти возвращает пустую строку, чтобы
// исключение не возникало на стороне Java.
static jstring toJavaString(JNIEnv *env, char *value) {
	if (value == NULL) {
		return (*env)->NewStringUTF(env, "");
	}
	jstring result = (*env)->NewStringUTF(env, value);
	HumanGramProxyFreeString(value);
	return result;
}

JNIEXPORT jint JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeStart(
		JNIEnv *env, jclass cls, jstring config) {
	(void) cls;
	if (config == NULL) {
		return HumanGramProxyStart(NULL);
	}
	const char *value = (*env)->GetStringUTFChars(env, config, NULL);
	if (value == NULL) {
		return 3;
	}
	jint result = HumanGramProxyStart(value);
	(*env)->ReleaseStringUTFChars(env, config, value);
	return result;
}

JNIEXPORT jstring JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeAddress(JNIEnv *env, jclass cls) {
	(void) cls;
	return toJavaString(env, HumanGramProxyAddr());
}

JNIEXPORT jint JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativePort(JNIEnv *env, jclass cls) {
	(void) env;
	(void) cls;
	return HumanGramProxyPort();
}

JNIEXPORT jint JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeSessions(JNIEnv *env, jobject obj) {
	(void) env;
	(void) obj;
	return HumanGramProxySessions();
}

JNIEXPORT jstring JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeLastError(JNIEnv *env, jclass cls) {
	(void) cls;
	return toJavaString(env, HumanGramProxyLastError());
}

JNIEXPORT void JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeFreeString(
		JNIEnv *env, jclass cls, jstring value) {
	// Строки возвращаются как jstring, память JVM освобождает сам.
	(void) env;
	(void) cls;
	(void) value;
}

JNIEXPORT jint JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeStop(JNIEnv *env, jclass cls) {
	(void) env;
	(void) cls;
	return HumanGramProxyStop();
}

JNIEXPORT jint JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeIsRunning(JNIEnv *env, jclass cls) {
	(void) env;
	(void) cls;
	return HumanGramProxyIsRunning();
}

JNIEXPORT jstring JNICALL
Java_org_telegram_messenger_HumanGramProxy_nativeVersion(JNIEnv *env, jclass cls) {
	(void) cls;
	return toJavaString(env, HumanGramProxyVersion());
}
*/
import "C"

// Ссылка на символ из cgo-преамбулы. Без неё компилятор сообщает,
// что импортированный пакет C не используется.
var _ = C.hgJniBridgePresent
