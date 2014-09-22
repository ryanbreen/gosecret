package gosecret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var gosecretRegex, _ = regexp.Compile("\\[(gosecret\\|[^\\]]*)\\]")

func createRandomBytes(length int) []byte {
	random_bytes := make([]byte, length)
	rand.Read(random_bytes)
	return random_bytes
}

// Create a random 256-bit array suitable for use as an AES-256 cipher key.
func CreateKey() []byte {
	return createRandomBytes(32)
}

// Create a random initialization vector to use for encryption.  Each gosecret tag should have a different
// initialization vector.
func CreateIV() []byte {
	return createRandomBytes(12)
}

func createCipher(key []byte) (cipher.AEAD, error) {
	aes, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(aes)
	if err != nil {
		return nil, err
	}
	return aesgcm, nil
}

func encrypt(plaintext []byte, key []byte, iv []byte, ad []byte) ([]byte, error) {
	aesgcm, err := createCipher(key)
	if err != nil {
		return nil, err
	}
	return aesgcm.Seal(nil, iv, plaintext, ad), nil
}

func decrypt(ciphertext []byte, key []byte, iv []byte, ad []byte) ([]byte, error) {
	aesgcm, err := createCipher(key)
	if err != nil {
		return nil, err
	}

	return aesgcm.Open(nil, iv, ciphertext, ad)
}

func decodeBase64(input []byte) ([]byte, error) {
	output := make([]byte, base64.StdEncoding.DecodedLen(len(input)))
	l, err := base64.StdEncoding.Decode(output, input)

	if err != nil {
		return nil, err
	}

	return output[:l], nil
}

func getBytesFromBase64File(filepath string) ([]byte, error) {
	file, err := ioutil.ReadFile(filepath)
	if err != nil {
		fmt.Println("Unable to read file", err)
		return nil, err
	}

	return decodeBase64(file)
}

func decryptTag(tagParts []string, keyroot string) ([]byte, error) {
	ct, err := base64.StdEncoding.DecodeString(tagParts[2])
	if err != nil {
		fmt.Println("Unable to decode ciphertext", tagParts[2], err)
		return nil, err
	}

	iv, err := base64.StdEncoding.DecodeString(tagParts[3])
	if err != nil {
		fmt.Println("Unable to decode IV", err)
		return nil, err
	}

	key, err := getBytesFromBase64File(filepath.Join(keyroot, tagParts[4]))
	if err != nil {
		fmt.Println("Unable to read file for decryption", err)
		return nil, err
	}

	plaintext, err := decrypt(ct, key, iv, []byte(tagParts[1]))
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

func encryptTag(tagParts []string, key []byte, keyname string) ([]byte, error) {
	iv := CreateIV()
	cipherText, err := encrypt([]byte(tagParts[2]), key, iv, []byte(tagParts[1]))
	if err != nil {
		return []byte(""), err
	}

	return []byte(fmt.Sprintf("[gosecret|%s|%s|%s|%s]",
		tagParts[1],
		base64.StdEncoding.EncodeToString(cipherText),
		base64.StdEncoding.EncodeToString(iv),
		keyname)), nil
}

// EncryptTags looks for any tagged data of the form [gosecret|authtext|plaintext] in the input content byte
// array and replaces each with an encrypted gosecret tag.  Note that the input content must be valid UTF-8.
// The second parameter is the name of the keyfile to use for encrypting all tags in the content, and the
// third parameter is the 256-bit key itself.
// EncryptTags returns a []byte with all unencrypted [gosecret] blocks replaced by encrypted gosecret tags.
func EncryptTags(content []byte, keyname, keyroot string, rotate bool) ([]byte, error) {

	if !utf8.Valid(content) {
		return nil, errors.New("File is not valid UTF-8")
	}

	match := gosecretRegex.Match(content)

	if match {

		keypath := filepath.Join(keyroot, keyname)
		key, err := getBytesFromBase64File(keypath)
		if err != nil {
			fmt.Println("Unable to read encryption key")
			return nil, err
		}

		content = gosecretRegex.ReplaceAllFunc(content, func(match []byte) []byte {
			parts := strings.Split(string(match), "|")
			lastPart := parts[len(parts)-1]
			lastPart = lastPart[:len(lastPart)-1]
			parts[len(parts)-1] = lastPart

			if len(parts) > 3 {
				if rotate {
					plaintext, err := decryptTag(parts, keyroot)
					if err != nil {
						fmt.Println("Unable to decrypt ciphertext", parts[2], err)
						return nil
					}

					parts[2] = string(plaintext)

					replacement, err := encryptTag(parts, key, keyname)
					if err != nil {
						fmt.Println("Failed to encrypt tag", err)
						return nil
					}
					return replacement
				} else {
					return match
				}
			} else {
				replacement, err := encryptTag(parts, key, keyname)
				if err != nil {
					fmt.Println("Failed to encrypt tag", err)
					return nil
				}
				return replacement
			}
		})
	}

	return content, nil
}

// DecryptTags looks for any tagged data of the form [gosecret|authtext|ciphertext|initvector|keyname] in the
// input content byte array and replaces each with a decrypted version of the ciphertext.  Note that the
// input content must be valid UTF-8.  The second parameter is the path to the directory in which keyfiles
// live.  For each |keyname| in a gosecret block, there must be a corresponding file of the same name in the
// keystore directory.
// DecryptTags returns a []byte with all [gosecret] blocks replaced by plaintext.
func DecryptTags(content []byte, keyroot string) ([]byte, error) {

	if !utf8.Valid(content) {
		return nil, errors.New("File is not valid UTF-8")
	}

	content = gosecretRegex.ReplaceAllFunc(content, func(match []byte) []byte {
		parts := strings.Split(string(match), "|")
		lastPart := parts[len(parts)-1]
		lastPart = lastPart[:len(lastPart)-1]
		parts[len(parts)-1] = lastPart

		if len(parts) < 5 {
			// Block is not encrypted.  Noop.
			return match
		} else {
			plaintext, err := decryptTag(parts, keyroot)
			if err != nil {
				fmt.Println("Unable to decrypt tag", err)
				return nil
			}

			return plaintext
		}
	})

	return content, nil
}
