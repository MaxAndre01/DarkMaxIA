package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	mathrand "math/rand"
	"os"
	"strings"
	"time"
)

var rng = mathrand.New(mathrand.NewSource(time.Now().UnixNano()))

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func randInt(min, max int) int { return rng.Intn(max-min) + min }

func randVar() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	n := randInt(6, 14)
	b := make([]byte, n)
	b[0] = letters[randInt(0, 26)]
	for i := 1; i < n; i++ {
		b[i] = letters[randInt(0, len(letters))]
	}
	return string(b)
}

func makeVars(count int) []string {
	used := map[string]bool{}
	vars := make([]string, count)
	for i := 0; i < count; i++ {
		v := randVar()
		for used[v] {
			v = randVar()
		}
		used[v] = true
		vars[i] = v
	}
	return vars
}

func jsNum(n int) string {
	switch randInt(0, 5) {
	case 0:
		return fmt.Sprintf("0x%X", n)
	case 1:
		return fmt.Sprintf("(%d^0)", n)
	case 2:
		return fmt.Sprintf("(%d|0)", n)
	case 3:
		return fmt.Sprintf("parseInt('%x',16)", n)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ── AES-256-GCM ──────────────────────────────────────────────────

func aesEncrypt(plaintext []byte) (ctB64, keyB64 string, err error) {
	seed := randBytes(32)
	h := sha256.Sum256(seed)
	key := h[:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return
	}
	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	ctB64 = base64.StdEncoding.EncodeToString(ct)
	keyB64 = base64.StdEncoding.EncodeToString(key)
	return
}

// ── XOR sobre los bytes del string base64 ────────────────────────

func xorLayer(data string, key []byte) string {
	src := []byte(data) // bytes del string b64
	out := make([]byte, len(src))
	kl := len(key)
	for i, b := range src {
		out[i] = b ^ key[i%kl] ^ byte(i%251) ^ byte((i*7+13)%256)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// ── Fragmentar en charCodes mezclados ────────────────────────────

func toCharCodes(s string) string {
	codes := make([]string, len(s))
	for i, c := range []byte(s) {
		codes[i] = jsNum(int(c))
	}
	return strings.Join(codes, ",")
}

func fragmentToJS(s, varName string) string {
	var parts []string
	i := 0
	for i < len(s) {
		size := randInt(12, 35)
		if i+size > len(s) {
			size = len(s) - i
		}
		parts = append(parts, fmt.Sprintf("String.fromCharCode(%s)", toCharCodes(s[i:i+size])))
		i += size
	}
	return fmt.Sprintf("var %s=[%s].join('');", varName, strings.Join(parts, ","))
}

// ── Ruido ────────────────────────────────────────────────────────

func makeNoise() string {
	nv := makeVars(12)
	lines := make([]string, 12)
	for i, v := range nv {
		lines[i] = fmt.Sprintf("var %s=%s;", v, jsNum(randInt(1000, 9999999)))
	}
	return strings.Join(lines, "\n")
}

// ── Obfuscator ───────────────────────────────────────────────────

func obfuscate(html string) (string, error) {
	v := makeVars(30)

	// Capa 1: AES-256-GCM
	ctB64, keyB64, err := aesEncrypt([]byte(html))
	if err != nil {
		return "", err
	}

	// Capa 2: XOR sobre el string base64 del AES output
	xorKey := randBytes(64)
	xorKeyB64 := base64.StdEncoding.EncodeToString(xorKey)
	xorData := xorLayer(ctB64, xorKey)
	// xorData es base64 de los bytes XOR-eados

	// Capa 3: fragmentar xorData en charCodes
	chunksJS := fragmentToJS(xorData, v[0])
	// v[0] = string base64 del xorData reconstruido

	// ── JS de descifrado ──────────────────────────────────────────
	//
	// Flujo correcto:
	//  1. v[0]  = xorData en base64  (reconstruido de charCodes)
	//  2. decode base64 de v[0]      → bytes XOR-eados
	//  3. revertir XOR byte a byte   → bytes del ctB64 string
	//  4. convertir esos bytes a string → ctB64
	//  5. decode base64 ctB64        → bytes del ciphertext AES
	//  6. WebCrypto AES-GCM decrypt  → HTML original
	//  7. document.write

	// nombres de vars para el JS
	vXorKeyB64  := v[1]  // la xorKey en b64 (hardcoded)
	vXorKeyArr  := v[2]  // Uint8Array de la xorKey
	vXorDataArr := v[3]  // Uint8Array de los bytes xor-eados
	vCtB64Arr   := v[4]  // Uint8Array resultado del XOR reverso = bytes del string ctB64
	vCtB64Str   := v[5]  // string ctB64 reconstruido
	vI          := v[6]  // loop index
	vKeyB64     := v[7]  // clave AES en b64 (hardcoded)
	vKeyArr     := v[8]  // Uint8Array de la clave AES
	vCtArr      := v[9]  // Uint8Array del ciphertext AES
	vCryptoKey  := v[10] // CryptoKey importada
	vNonce      := v[11] // primeros 12 bytes = nonce
	vCipher     := v[12] // resto = ciphertext real
	vDecrypted  := v[13] // ArrayBuffer resultado
	vHtml       := v[14] // string HTML final

	// Anti-devtools
	vDbgFlag    := v[15]
	vDbgTimer   := v[16]
	vDbgT       := v[17]

	// Console trap
	vNoop       := v[18]

	antiDbg := fmt.Sprintf(`(function(){
var %s=false;
var %s=setInterval(function(){
  var %s=+new Date();debugger;
  if((+new Date()-%s)>100){%s=true;document.body.innerHTML='';clearInterval(%s);window.location='about:blank';}
},600);})();`, vDbgFlag, vDbgTimer, vDbgT, vDbgT, vDbgFlag, vDbgTimer)

	consoleTrap := fmt.Sprintf(`(function(){
var %s=function(){};
console.log=%s;console.warn=%s;console.dir=%s;console.info=%s;
})();`, vNoop, vNoop, vNoop, vNoop, vNoop)

	// Paso 1-4: revertir XOR → recuperar ctB64 como string
	revertXOR := fmt.Sprintf(`
var %s='%s';
var %s=(function(){var _a=atob(%s),_b=new Uint8Array(_a.length);for(var _i=0;_i<_a.length;_i++){_b[_i]=_a.charCodeAt(_i);}return _b;})();
var %s=(function(){var _a=atob(%s),_b=new Uint8Array(_a.length);for(var _i=0;_i<_a.length;_i++){_b[_i]=_a.charCodeAt(_i);}return _b;})();
var %s=new Uint8Array(%s.length);
for(var %s=0;%s<%s.length;%s++){
  %s[%s]=%s[%s]^%s[%s%%%s.length]^(%s%%251)^(((%s*7+13)%%256));
}
var %s=String.fromCharCode.apply(null,%s);`,
		vXorKeyB64, xorKeyB64,
		vXorKeyArr, vXorKeyB64,
		vXorDataArr, v[0],
		vCtB64Arr, vXorDataArr,
		vI, vI, vXorDataArr, vI,
		vCtB64Arr, vI, vXorDataArr, vI, vXorKeyArr, vI, vXorKeyArr,
		vI,
		vI,
		vCtB64Str, vCtB64Arr)

	// Paso 5-7: AES-GCM decrypt
	aesDecrypt := fmt.Sprintf(`
var %s='%s';
var %s=(function(){var _a=atob(%s),_b=new Uint8Array(_a.length);for(var _i=0;_i<_a.length;_i++){_b[_i]=_a.charCodeAt(_i);}return _b;})();
var %s=(function(){var _a=atob(%s),_b=new Uint8Array(_a.length);for(var _i=0;_i<_a.length;_i++){_b[_i]=_a.charCodeAt(_i);}return _b;})();
window.crypto.subtle.importKey('raw',%s,{name:'AES-GCM'},false,['decrypt'])
.then(function(%s){
  var %s=%s.slice(0,12);
  var %s=%s.slice(12);
  return window.crypto.subtle.decrypt({name:'AES-GCM',iv:%s},%s,%s);
})
.then(function(%s){
  var %s=new TextDecoder().decode(%s);
  document.open();document.write(%s);document.close();
})
.catch(function(e){console.error(e);document.body.innerHTML='<pre>ERROR: '+e+'</pre>';});`,
		vKeyB64, keyB64,
		vKeyArr, vKeyB64,
		vCtArr, vCtB64Str,
		vKeyArr,
		vCryptoKey,
		vNonce, vCtArr,
		vCipher, vCtArr,
		vNonce, vCryptoKey, vCipher,
		vDecrypted,
		vHtml, vDecrypted,
		vHtml)

	script := fmt.Sprintf("%s\n%s\n(function(){\n'use strict';\n%s\n%s\n%s\n%s\n%s\n})();",
		makeNoise(), consoleTrap, antiDbg, chunksJS, revertXOR, aesDecrypt, makeNoise())

	return fmt.Sprintf("<!DOCTYPE html><html><head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>.</title></head><body><script>%s</script></body></html>", script), nil
}

// ── Main ─────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		fmt.Println("╔══════════════════════════════════════════╗")
		fmt.Println("║   DarkMax HTML Obfuscator                ║")
		fmt.Println("╠══════════════════════════════════════════╣")
		fmt.Println("║  Uso:    go run encrypter.go input.html  ║")
		fmt.Println("║  Output: input.obf.html                  ║")
		fmt.Println("╚══════════════════════════════════════════╝")
		os.Exit(1)
	}

	inFile := os.Args[1]
	raw, err := os.ReadFile(inFile)
	if err != nil {
		fmt.Printf("❌ Error leyendo archivo: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🔐 Procesando %s (%d bytes)...\n", inFile, len(raw))
	fmt.Println("   ✓ Capa 1: AES-256-GCM")
	fmt.Println("   ✓ Capa 2: XOR rotante 64 bytes")
	fmt.Println("   ✓ Capa 3: Fragmentación aleatoria")
	fmt.Println("   ✓ Capa 4: CharCode encoding mixto")
	fmt.Println("   ✓ Capa 5: Anti-devtools debugger trap")
	fmt.Println("   ✓ Capa 6: Console poisoning")

	result, err := obfuscate(string(raw))
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}

	outFile := strings.TrimSuffix(inFile, ".html") + ".obf.html"
	if err := os.WriteFile(outFile, []byte(result), 0644); err != nil {
		fmt.Printf("❌ Error guardando: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Listo → %s\n", outFile)
	fmt.Printf("   Original:   %d bytes\n", len(raw))
	fmt.Printf("   Obfuscado:  %d bytes\n", len(result))
	fmt.Printf("   Expansión:  %.0f%%\n", float64(len(result))/float64(len(raw))*100)
}