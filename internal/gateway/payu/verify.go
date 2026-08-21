package payu

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"
)

// VerifyResponseHash verifies the reverse hash in a PayU response.
// PayU prefixes the hash input with additionalCharges when that field is present.
func VerifyResponseHash(params map[string]string, salt string) bool {
	receivedHash := params["hash"]
	if receivedHash == "" {
		return false
	}

	computed := computeResponseHash(params, salt)
	return strings.EqualFold(computed, receivedHash)
}

func computeResponseHash(params map[string]string, salt string) string {
	key := params["key"]
	txnid := params["txnid"]
	amount := params["amount"]
	productinfo := params["productinfo"]
	firstname := params["firstname"]
	email := params["email"]
	status := params["status"]
	udf1 := params["udf1"]
	udf2 := params["udf2"]
	udf3 := params["udf3"]
	udf4 := params["udf4"]
	udf5 := params["udf5"]
	additionalCharges := params["additionalCharges"]

	var hashString string
	if additionalCharges != "" {
		hashString = fmt.Sprintf("%s|%s|%s||||||%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
			additionalCharges, salt, status,
			udf5, udf4, udf3, udf2, udf1,
			email, firstname, productinfo, amount, txnid, key)
	} else {
		hashString = fmt.Sprintf("%s|%s||||||%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
			salt, status,
			udf5, udf4, udf3, udf2, udf1,
			email, firstname, productinfo, amount, txnid, key)
	}

	h := sha512.New()
	h.Write([]byte(hashString))
	return hex.EncodeToString(h.Sum(nil))
}
