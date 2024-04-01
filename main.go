package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Configuration struct to hold configuration values
type Configuration struct {
	SuacoinRPCNodeURL string
	SuacoinRPCUser    string
	SuacoinRPCPass    string
	SuatokensNodeURL  string
	DatabaseName      string
}

// GasFeeResponse struct to hold the response for the /gasfee API
type GasFeeResponse struct {
	AvgTxFee float64 `json:"avgtxfee"`
}

// BlockCountResponse struct to hold the response for the /blockcount API
type BlockCountResponse struct {
	Block int `json:"block"`
}

type ValidateAddressResult struct {
	IsValid      bool   `json:"isvalid"`
	Address      string `json:"address"`
	IsMine       bool   `json:"ismine"`
	IsScript     bool   `json:"isscript"`
	PubKey       string `json:"pubkey"`
	IsCompressed bool   `json:"iscompressed"`
	Account      string `json:"account"`
}

type ValidateAddressResponse struct {
	Result *ValidateAddressResult `json:"result"`
	Error  interface{}            `json:"error"`
	ID     string                 `json:"id"`
}

// IPResponse represents the JSON response from the IPify service.
type IPResponse struct {
	IP string `json:"ip"`
}

// Token structure
type Token struct {
	ID         string               `json:"id"`
	Ticker     string               `json:"ticker"`
	Name       string               `json:"name"`
	Whitepaper string               `json:"whitepaper"`
	Hash       string               `json:"hash"`
	Signature  string               `json:"signature"`
	Block      string               `json:"block"`
	Decimals   int                  `json:"decimals"`
	Quantity   primitive.Decimal128 `json:"quantity"`
	Status     bool                 `json:"status"`
	Mintable   bool                 `json:"mintable"`
	Minter     string               `json:"minter"`
	Payer      string               `json:"payer"`
	Date       time.Time            `json:"date"`
}

// TokenCreateRequest represents the request JSON for creating a new token
type TokenCreateRequest struct {
	Ticker     string `json:"ticker"`
	Name       string `json:"name"`
	Whitepaper string `json:"whitepaper"`
	Hash       string `json:"hash"`
	Block      string `json:"block"`
	Decimals   int    `json:"decimals"`
	Quantity   int    `json:"quantity"`
	Mintable   bool   `json:"mintable"`
	Minter     string `json:"minter"`
	Payer      string `json:"payer"`
	Signature  string `json:"signature"`
}

// Mint structure
type Mint struct {
	ID        string               `json:"id"`
	Token     string               `json:"token"`
	Block     string               `json:"block"`
	Signature string               `json:"signature"`
	Previous  primitive.Decimal128 `json:"previous"`
	Status    bool                 `json:"status"`
	Hash      string               `json:"hash"`
	Amount    primitive.Decimal128 `json:"amount"`
	Quantity  primitive.Decimal128 `json:"quantity"`
	Payer     string               `json:"payer"`
	Date      time.Time            `json:"date"`
}

// Transfer structure
type Transfer struct {
	ID        string               `json:"id"`
	Token     string               `json:"token"`
	To        string               `json:"to"`
	Amount    primitive.Decimal128 `json:"amount"`
	Block     string               `json:"block"`
	Status    bool                 `json:"status"`
	From      string               `json:"from"`
	Signature string               `json:"signature"`
	Hash      string               `json:"hash"`
	Payer     string               `json:"payer"`
	Date      time.Time            `json:"date"`
}

// Balance structure
type Balance struct {
	Address  string               `json:"address"`
	Txid     string               `json:"txid"`
	Token    string               `json:"token"`
	Quantity primitive.Decimal128 `json:"quantity"`
	Balance  primitive.Decimal128 `json:"balance"`
	Status   bool                 `json:"status"`
	Block    string               `json:"block"`
	Date     time.Time            `json:"date"`
	LastSync time.Time            `json:"lastSync"`
}

// Node structure
type Node struct {
	Address  string    `json:"address"`
	LastSync time.Time `json:"lastSync"`
}

var (
	mongoClient  *mongo.Client
	tokens       *mongo.Collection
	nodes        *mongo.Collection
	mints        *mongo.Collection
	transfers    *mongo.Collection
	balances     *mongo.Collection
	mutex        = &sync.Mutex{}
	isLeader     = false
	leaderAddr   = ""
	tokenList    = make(map[string]Token)
	mintList     = make(map[string]Mint)
	transferList = make(map[string]Transfer)
	balanceList  = make(map[string]Balance)
	nodeList     = make(map[string]Node)
)

// initMongo initializes the MongoDB client and collection
func initMongo(dbname string) {
	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = client.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}

	mongoClient = client

	db := client.Database(dbname)
	tokens = db.Collection("tokens")
	nodes = db.Collection("nodes")
	mints = db.Collection("mints")
	transfers = db.Collection("transfers")
	balances = db.Collection("balances")
}

// LoadConfig loads configuration from the .env file
func LoadConfig() Configuration {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	return Configuration{
		SuacoinRPCNodeURL: os.Getenv("suacoin_rpcnode_url"),
		SuacoinRPCUser:    os.Getenv("suacoin_rpcuser"),
		SuacoinRPCPass:    os.Getenv("suacoin_rpcpass"),
		SuatokensNodeURL:  os.Getenv("suatokens_node_url"),
		DatabaseName:      os.Getenv("datbasename"),
	}
}

// MakeRPCRequest makes an RPC request to the Bitcoin server
func MakeRPCRequest(reqtype string, method string, params []interface{}, config Configuration) ([]byte, error) {
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	// Create the request payload using json.Marshal
	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  method,
		"params":  params,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Create the request
	request, err := http.NewRequest(reqtype, rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return nil, err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func getRawMempool() (string, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "getrawmempool",
		"params":  []interface{}{},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "{}", err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "{}", err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "{}", err
	}

	// Convert the response body to a string
	bodyString := string(body)

	return bodyString, nil
}

// validatePayerAddress validates if the given address is a mine address
func validatePayerAddress(address string) (bool, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "validateaddress",
		"params":  []interface{}{address},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return false, err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "application/json;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return false, err
	}

	// Parse the response JSON
	var validateAddressResponse ValidateAddressResponse
	err = json.Unmarshal(body, &validateAddressResponse)
	if err != nil {
		fmt.Println("Error unmarshalling JSON:", err)
		return false, err
	}

	// Debug prints for inspection
	fmt.Printf("Response JSON: %s\n", string(body))
	fmt.Printf("Parsed Response: %+v\n", validateAddressResponse)

	// Check if the address is valid and is mine
	return validateAddressResponse.Result.IsValid && validateAddressResponse.Result.IsMine, nil
}

func readerFromString(s string) *ReadCloser {
	return &ReadCloser{strings.NewReader(s)}
}

type ReadCloser struct {
	io.Reader
}

func (rc *ReadCloser) Close() error {
	return nil
}

func getMempoolEntry(txID string) (string, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "getrawtransaction",
		"params":  []interface{}{txID, 1},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "{}", err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "{}", err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "{}", err
	}

	// Convert the response body to a string
	bodyString := string(body)

	return bodyString, nil
}

func getTxOut(txID string, vOut string) (string, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "gettxout",
		"params":  []interface{}{txID, vOut},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "{}", err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "{}", err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "{}", err
	}

	// Convert the response body to a string
	bodyString := string(body)

	return bodyString, nil
}

// GetGasFee returns the average transaction fee from the Bitcoin server
func GetGasFee(config Configuration) (float64, error) {
	// Get the list of transaction IDs in the memory pool
	mempoolResponse, err := getRawMempool()
	if err != nil {
		return 0, err
	}

	if strings.Contains(mempoolResponse, "Parse error") {
		fmt.Println("Response contains the word 'Parse error'.")
		return 0, err
	}

	// Unmarshal the mempool response to a map
	var mempool map[string]interface{}
	err = json.Unmarshal([]byte(mempoolResponse), &mempool)
	if err != nil {
		return 0, err
	}

	// Extract the "result" field from the mempool response
	result, ok := mempool["result"]
	if !ok {
		return 0, errors.New("Missing 'result' field in mempool response")
	}

	// Convert the "result" field to JSON representation
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return 0, err
	}

	// Unmarshal the JSON representation of "result" to get transaction IDs
	var txIDs []string
	err = json.Unmarshal(resultJSON, &txIDs)
	if err != nil {
		return 0, err
	}

	// Calculate the average transaction fee
	totalFee := 0.0
	for _, txID := range txIDs {
		// Get details about each transaction in the memory pool
		entryResponse, err := getMempoolEntry(txID)
		if err != nil {
			// Log the error but continue to the next transaction
			log.Printf("Error fetching details for transaction %s: %v", txID, err)
			continue
		}

		// Convert the string to []byte
		entryBytes := []byte(entryResponse)

		var transaction map[string]interface{}
		err = json.Unmarshal(entryBytes, &transaction)
		if err != nil {
			// Log the error but continue to the next transaction
			log.Printf("Error decoding details for transaction %s: %v", txID, err)
			continue
		}

		// Calculate the total input value
		var totalInputValue float64
		inputs, ok := transaction["vin"].([]interface{})
		if ok {

			for _, input := range inputs {
				sourceTxID := input.(map[string]interface{})["txid"].(string)
				vout := int(input.(map[string]interface{})["vout"].(float64))

				// Fetch the output value from the blockchain using the gettxout RPC call
				outputValue, err := getTxOut(sourceTxID, fmt.Sprintf("%d", vout))
				if err != nil {
					// Handle error, log, etc.
					fmt.Printf("Error fetching output value for sourceTxID %s, vout %d: %v\n", sourceTxID, vout, err)
					continue
				}

				// Convert the output value to float64
				outputValueFloat, err := strconv.ParseFloat(outputValue, 64)
				if err != nil {
					// Handle error, log, etc.
					fmt.Printf("Error converting output value to float64: %v\n", err)
					continue
				}

				// Add the output value to the input sum
				totalInputValue += outputValueFloat
			}
		}

		// Calculate the total output value
		var totalOutputValue float64
		outputs, ok := transaction["vout"].([]interface{})
		if ok {
			for _, output := range outputs {
				outputValue, ok := output.(map[string]interface{})["value"].(float64)
				if ok {
					totalOutputValue += outputValue
				}
			}
		}

		// Calculate the fee
		fee := totalInputValue - totalOutputValue
		fmt.Printf("Transaction Fee: %.8f SUA\n", fee)

		totalFee += fee
	}

	// Calculate the average fee
	avgFee := 0.0
	if len(txIDs) > 0 {
		avgFee = totalFee / float64(len(txIDs))
	}

	if avgFee == 0.0 {
		avgFee = 0.00200000
	}

	return avgFee, nil
}

// GasFeeHandler handles the /gasfee API request
func GasFeeHandler(w http.ResponseWriter, r *http.Request) {
	config := LoadConfig()

	// Get the average transaction fee
	avgFee, err := GetGasFee(config)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Create and send the JSON response
	response := GasFeeResponse{
		AvgTxFee: avgFee,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Function to check account balance
func checkAccountBalance(accountLabel string, confirmations int) (float64, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "getbalance",
		"params":  []interface{}{accountLabel, confirmations},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return 0, err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return 0, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return 0, err
	}

	// Extract the account from the response
	balance, ok := result["result"].(float64)
	if !ok {
		return 0, fmt.Errorf("failed to get balance")
	}

	return balance, nil

}

// Function to send a transaction
func sendTransaction(fromAccount string, toAddress string, amount float64, comment string) (string, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	var payload = map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "sendfrom",
		"params":  []interface{}{fromAccount, toAddress, amount, comment},
	}

	if fromAccount == "*" {
		payload = map[string]interface{}{
			"jsonrpc": "1.0",
			"id":      "suatokensnode",
			"method":  "sendtoaddress",
			"params":  []interface{}{toAddress, amount, comment},
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "{}", err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "{}", err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	// Unmarshal the JSON response
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	txid, ok := result["result"].(string)
	if !ok {
		return "", fmt.Errorf("Failed to capture txid from the response")
	}
	return txid, nil


}

// getBlockHeight sends an RPC request to the suacoin node to get the block height for a given block hash.
func getBlockHeight(blockHash string) (int64, error) {
	// Replace with the actual suacoin RPC server URL from your .env file
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	// Create RPC request payload
	data := fmt.Sprintf(`{"jsonrpc": "1.0", "id": "getblockheight", "method": "getblock", "params": ["%s"]}`, blockHash)

	// Create HTTP request
	req, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(data))
	if err != nil {
		return 0, err
	}

	// Set RPC credentials
	req.SetBasicAuth(rpcUser, rpcPass)
	req.Header.Set("Content-Type", "text/plain;")

	// Send HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Read response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	// Parse JSON response
	var response map[string]interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, err
	}

	// Check for errors in the JSON response
	if response["error"] != nil {
		errMessage, _ := response["error"].(map[string]interface{})["message"].(string)
		return 0, errors.New("RPC error: " + errMessage)
	}

	// Extract block height from the JSON response
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		return 0, errors.New("Invalid RPC response format")
	}

	blockHeight, ok := result["height"].(float64)
	if !ok {
		return 0, errors.New("Block height not found in RPC response")
	}

	return int64(blockHeight), nil
}

func DigestString(s *string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(*s)))
}

func md5String(s string) string {
	hash := md5.New()
	hash.Write([]byte(s))
	return hex.EncodeToString(hash.Sum(nil))
}

// generateTokenHash generates an MD5 hash from the token information.
func generateTokenHash2(token Token) (string, error) {

	tokenJSON := fmt.Sprintf(`{"ticker" : "%s", "name": "%s", "whitepaper": "%s", "block": "%s", "decimals": %d, "quantity": "%s", "mintable": %t, "minter": "%s"}`,
		token.Ticker, token.Name, token.Whitepaper,
		token.Block, token.Decimals, token.Quantity,
		token.Mintable, token.Minter)

	return md5String(tokenJSON), nil
}

// verifyTokenHash verifies if the given token's Hash is equal to md5(ticker, name, whitepaper, block, decimals, signature, quantity, minter, mintable)
func verifyTokenHash(token Token) bool {
	// Create the expected hash
	expectedHash := generateTokenHash(token)

	// Compare the expected hash with the actual hash
	return token.Hash == expectedHash
}

func generateTokenHash(token Token) string {
	hash := md5.New()
	// Concatenate the properties to create the input for the hash
	input := fmt.Sprintf("%s%s%s%s%d%s%t%s",
		token.Ticker, token.Name, token.Whitepaper,
		token.Block, token.Decimals, token.Quantity,
		token.Mintable, token.Minter)

	// Write the input to the hash
	hash.Write([]byte(input))

	// Return the hex-encoded hash
	return hex.EncodeToString(hash.Sum(nil))
}

func generateMintHash2(mint Mint) (string, error) {
	// Convert the token to a JSON string
	tokenJSON, err := json.Marshal(mint)
	if err != nil {
		return "", err
	}

	// Calculate MD5 hash
	hasher := md5.New()
	hasher.Write(tokenJSON)
	hash := hasher.Sum(nil)

	// Convert the hash to a hex string
	hashString := hex.EncodeToString(hash)

	return hashString, nil
}

// verifyMintHash verifies if the given mint's Hash is equal to md5(tokenid, previous, amount, signature, block, quantity)
func verifyMintHash(mint Mint) bool {
	// Create the expected hash
	expectedHash := generateMintHash(mint)

	// Compare the expected hash with the actual hash
	return mint.Hash == expectedHash
}

func generateMintHash(mint Mint) string {
	hash := md5.New()
	// Concatenate the properties to create the input for the hash
	input := fmt.Sprintf("%s%d%s%s%s",
		mint.Token, mint.Previous, mint.Amount.String(),
		mint.Block, mint.Quantity.String())

	// Write the input to the hash
	hash.Write([]byte(input))

	// Return the hex-encoded hash
	return hex.EncodeToString(hash.Sum(nil))
}

// generateTransferHash generates an MD5 hash from the transfer information.
func generateTransferHash2(transfer Transfer) (string, error) {
	// Convert the token to a JSON string
	tokenJSON, err := json.Marshal(transfer)
	if err != nil {
		return "", err
	}

	// Calculate MD5 hash
	hasher := md5.New()
	hasher.Write(tokenJSON)
	hash := hasher.Sum(nil)

	// Convert the hash to a hex string
	hashString := hex.EncodeToString(hash)

	return hashString, nil
}

// verifyTransferHash verifies if the given token's Hash is equal to md5(tokenid, to, from, signature, block, amount)
func verifyTransferHash(transfer Transfer) bool {
	// Create the expected hash
	expectedHash := generateTransferHash(transfer)

	// Compare the expected hash with the actual hash
	return transfer.Hash == expectedHash
}

func generateTransferHash(transfer Transfer) string {
	hash := md5.New()
	// Concatenate the properties to create the input for the hash
	// $data = $_POST['token'].$_POST['to'].$_POST['from'].$_POST['block'].$_POST['qty'];
	input := fmt.Sprintf("%s%s%s%s%s",
		transfer.Token, transfer.To, transfer.From, transfer.Block, transfer.Amount.String())

	// Write the input to the hash
	hash.Write([]byte(input))

	// Return the hex-encoded hash
	return hex.EncodeToString(hash.Sum(nil))
}

// Function to get the account for a given address
func getAccountForAddress(address string) (string, error) {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	// Create the request
	requestBody := fmt.Sprintf(`{"jsonrpc": "1.0", "id": "suatokensnode", "method": "getaccount", "params": ["%s"]}`, address)
	request, err := http.NewRequest("POST", rpcNodeURL, nil)
	if err != nil {
		return "", err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)

	// Set the request body
	request.Body = ioutil.NopCloser(readerFromString(requestBody))
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	// Parse the response JSON
	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}

	if result["result"] == nil {
		return "*", nil
	}

	// Extract the account from the response
	account, ok := result["result"].(string)
	if !ok {
		return "", fmt.Errorf("failed to get account for address %s", address)
	}

	// Check for the empty account string and return a specific error
	if account == "" {
		return "", nil
	}

	return account, nil
}

func setTransactionFee(fee float64) error {
	// Replace with your RPC user and password
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "settxfee",
		"params":  []interface{}{fee},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return err
	}

	// Set basic authentication
	request.SetBasicAuth(rpcUser, rpcPass)
	request.Header.Set("Content-Type", "text/plain;")

	// Make the request
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Check the HTTP status code
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Unexpected status code: %d", response.StatusCode)
	}

	return nil
}

func getTokenDecimals(newToken string) (int, error) {
	var existingToken Token
	objID, err := primitive.ObjectIDFromHex(newToken)
	if err != nil {
		return 2, nil
	}

	err = tokens.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&existingToken)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// No existing document found
			return 2, nil // You might want to return a specific error to distinguish from not found and other errors
		}
		// An error occurred while querying the database
		return 2, err
	}

	// Token found, return its decimals
	return existingToken.Decimals, nil
}

// createToken handles the creation of a new token
func createToken(w http.ResponseWriter, r *http.Request) {

	if !isLeader {
		http.Error(w, "Not the leader. Cannot create token.", http.StatusForbidden)
		return
	}

	config := LoadConfig()

	var newToken Token
	if err := json.NewDecoder(r.Body).Decode(&newToken); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Convert newTransfer.Amount to string
	amountStr := newToken.Quantity.String()

	// Retrieve the number of decimals for the token
	decimals, err := getTokenDecimals(newToken.ID)
	if err != nil {
		// Handle the error
		decimals = 2
	}

	// Check if the string representation contains a decimal point
	if !strings.Contains(amountStr, ".") {
		if decimals > 0 && decimals <= 8 {
			amountStr += "." + strings.Repeat("0", decimals)
		}
	}

	numDecimals := countDecimals(amountStr)

	if numDecimals != decimals {
		http.Error(w, "Bad Request: Invalid amount", http.StatusBadRequest)
		return
	}

	// Convert newToken.Quantity to primitive.Decimal128
	amount, err := primitive.ParseDecimal128(amountStr)
	if err != nil {
		http.Error(w, "Bad Request: Invalid amount", http.StatusBadRequest)
		return
	}
	newToken.Quantity = amount

	// Now, you can use the CompareDecimal128 function to compare existingBalanceSender.Balance and newTransfer.Amount to 0.0
	zero := primitive.NewDecimal128(0, 0)

	amountResult, err := CompareDecimal128(newToken.Quantity, zero)
	if err != nil {
		// Handle the error
		http.Error(w, "Bad Request: Invalid request body", http.StatusBadRequest)
		return
	}

	if amountResult == -1 {
		fmt.Println("Quantity is less than or equal to 0.0")
		// Handle the case where either balance or amount is less than or equal to 0.0
		http.Error(w, "Bad Request: Quantity is less than or equal to 0.0", http.StatusBadRequest)
		return
	}

	formattedPayer := newToken.Payer
	fmt.Printf("\"%s\"", formattedPayer)

	isMine, err := validatePayerAddress(formattedPayer)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	if isMine {
		fmt.Println("Payer Address is a mine address to this node")
	} else {
		fmt.Println("Payer Address is not a mine address to this node")
		return
	}

	// Check if tx already in Database exit and provide error
	var existingTxIdToken Token
	err = tokens.FindOne(context.Background(), bson.M{"ticker": newToken.Ticker,
		"name":       newToken.Name,
		"whitepaper": newToken.Whitepaper}).Decode(&existingTxIdToken)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// No existing document found, continue processing
		} else {
			// An error occurred while querying the database, handle it accordingly
			// continue processing
		}
	} else {
		// Transaction already exists in the database, provide an error and exit
		fmt.Println("Token already exists in the blockchain")
		http.Error(w, "Token already exists in the blockchain", http.StatusUnauthorized)
		return
	}

	// Check if block height is provided and valid
	if newToken.Block != "" {
		blockHash := newToken.Block

		// Call suacoin RPC to get block height from the given block hash
		blockHeight, err := getBlockHeight(blockHash)
		if err != nil {
			log.Printf("Error getting block height: %v", err)
			http.Error(w, "Error getting block height", http.StatusInternalServerError)
			return
		}

		config := LoadConfig()

		// Get the block count from the Suacoin RPC server
		currentBlockHeight, err := GetBlockCount(config)
		if err != nil {
			log.Printf("Error getting current block height: %v", err)
			http.Error(w, "Error getting current block height", http.StatusInternalServerError)
			return
		}

		// Check if the provided block hash corresponds to a block height before the current block height
		if blockHeight > int64(currentBlockHeight) {
			http.Error(w, "Block hash corresponds to a block after the current block height", http.StatusBadRequest)
			return
		}
	}

	// Verify the signature
	err1 := verifySignatureWithRPC(newToken.Minter, newToken.Signature, newToken.Hash)
	if err1 == false {
		http.Error(w, "Signature verification failed", http.StatusUnauthorized)
		return
	}

	isValid := verifyTokenHash(newToken)
	fmt.Println("Is Token Hash Valid?", isValid)

	if isValid == false {
		http.Error(w, "Hash verification failed", http.StatusUnauthorized)
		return
	}

	confirmations := 6

	// Get the account for the payer address
	accountLabel, err := getAccountForAddress(newToken.Payer)
	if err != nil {
		fmt.Println("Error getting account:", err)
		return
	}

	fmt.Printf("Account for address %s: %s\n", newToken.Payer, accountLabel)

	// Check account balance
	balance, err := checkAccountBalance(accountLabel, confirmations)
	if err != nil {
		fmt.Println("Error checking account balance:", err)
		http.Error(w, "Error checking account balance", http.StatusUnauthorized)
		return
	}

	// Get the average transaction fee
	avgFee, err := GetGasFee(config)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var baseCost = "9500.00"
	if newToken.Mintable {
		baseCost = "9500.00"
	} else {
		baseCost = "100.00"
	}

	tokenCreationCost, err := strconv.ParseFloat(baseCost, 64)
	if err != nil {
		fmt.Println("Error parsing float:", err)
		return
	}

	tokenCreationCostRequired := tokenCreationCost + avgFee

	if balance >= tokenCreationCostRequired {

		err := setTransactionFee(avgFee)
		if err != nil {
			log.Println("Error setting transaction fee:", err)
			return
		}

		var createTokenString = fmt.Sprintf("createToken %s, %s, %s, %d, %s",
			newToken.Ticker,
			newToken.Name,
			newToken.Quantity.String(),
			newToken.Decimals,
			newToken.Whitepaper,
		)

		createTokenString = md5String(createTokenString)

		txid, err1 := sendTransaction(accountLabel, "1HCPenJjjxSKszJdQavKZCGxphZdkGW4Nj", tokenCreationCost, createTokenString)
		if err1 != nil {
			fmt.Println("Error sending transaction:", err1)
			return
		}

		fmt.Println("Transaction sent successfully")
		newToken.ID = txid

	} else {
		fmt.Println("Insufficient balance")
		http.Error(w, "Insufficient balance", http.StatusUnauthorized)
		return
	}

	newToken.Status = true
	newToken.Date = time.Now()

	mutex.Lock()
	defer mutex.Unlock()

	// Insert the new token into MongoDB
	_, err2 := tokens.InsertOne(context.Background(), newToken)
	if err2 != nil {
		log.Printf("Error inserting token into MongoDB: %v", err2)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	newBalance := Balance{
		Address:  newToken.Minter,
		Txid:     newToken.ID,
		Token:    newToken.ID,
		Quantity: newToken.Quantity,
		Balance:  newToken.Quantity,
		Status:   true,
		Block:    newToken.Block,
		Date:     time.Now(),
		LastSync: time.Now(),
	}

	_, err3 := balances.InsertOne(context.Background(), newBalance)
	if err3 != nil {
		log.Printf("Error inserting balance into MongoDB: %v", err3)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Respond with success message
	json.NewEncoder(w).Encode(fmt.Sprintf("Token with ID %s created", newToken.ID))
}

// tokenExistsLocally checks if a token exists in the local MongoDB database
func tokenExistsLocally(tokenID string) bool {
	filter := bson.M{"id": tokenID}

	// Fetch the list of tokens from MongoDB
	cur, err := tokens.Find(context.Background(), filter)
	if err != nil {
		// Error occurred while fetching tokens
		return false
	}
	defer cur.Close(context.Background())

	// Iterate over the cursor to check if the token exists
	for cur.Next(context.Background()) {
		var token Token
		if err := cur.Decode(&token); err != nil {
			// Error decoding token
			return false
		}
		// If token with the given ID is found, return true
		if token.ID == tokenID {
			return true
		}
	}

	// Token with the given ID not found
	return false
}

// listTokens returns the list of tokens
func listTokens(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of tokens from MongoDB
	cur, err := tokens.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching tokens from MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode tokens from the cursor
	tokenList = make(map[string]Token)
	for cur.Next(context.Background()) {
		var token Token
		err := cur.Decode(&token)
		if err != nil {
			log.Printf("Error decoding token from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		tokenList[token.ID] = token
	}

	// Respond with the list of tokens in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenList)
}

// GetBlockCount returns the block count from the Suacoin RPC server
func GetBlockCount(config Configuration) (int, error) {
	var url = config.SuacoinRPCNodeURL
	user := config.SuacoinRPCUser
	pass := config.SuacoinRPCPass
	url = strings.TrimPrefix(url, "http://")
	url = "http://" + url

	payload := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "suatokensnode",
		"method":  "getblockcount",
		"params":  []interface{}{},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return 0, err
	}

	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "text/plain;")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	// Unmarshal the response JSON
	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return 0, err
	}

	// Extract the block count from the response
	blockCount, ok := response["result"].(float64)
	if !ok {
		return 0, fmt.Errorf("error extracting block count from response")
	}

	return int(blockCount), nil
}

// BlockCountHandler handles the /blockcount API request
func BlockCountHandler(w http.ResponseWriter, r *http.Request) {
	config := LoadConfig()

	// Get the block count from the Suacoin RPC server
	blockCount, err := GetBlockCount(config)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Create and send the JSON response
	response := BlockCountResponse{
		Block: blockCount,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// leaderElection periodically checks if the current node should become the leader
func leaderElection() {
	for {
		time.Sleep(5 * time.Second) // Adjust the interval as needed

		mutex.Lock()
		if !isLeader {
			isLeader = true
			leaderAddr = getExternalIP() // Implement your function to get the external IP
			log.Printf("Node elected as leader: %s", leaderAddr)

			// Notify other nodes to sync
			go syncTokenList()
			go syncMintList()
			go syncTransferList()
			go syncBalanceList()
		}
		mutex.Unlock()
	}
}

// getOtherNodeAddresses retrieves the addresses of other nodes from MongoDB
func getOtherNodeAddresses() []string {
	allSeedNodes := []string{
		"asia.suatokens.com",
		"africa.suatokens.com",
		"na.suatokens.com",
		"seed.suatokens.com",
		"sa.suatokens.com",
		"eu.suatokens.com",
		"au.suatokens.com",
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of nodes from MongoDB
	cur, err := nodes.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching nodes from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode nodes from the cursor
	var otherNodes []string
	for cur.Next(context.Background()) {
		var node Node
		err := cur.Decode(&node)
		if err != nil {
			log.Printf("Error decoding node from MongoDB: %v", err)
			return nil
		}

		// Exclude the current node's address from the list
		if node.Address != leaderAddr {
			otherNodes = append(otherNodes, node.Address)
		}
	}

	// Shuffle the list of nodes randomly
	rand.Shuffle(len(allSeedNodes), func(i, j int) {
		allSeedNodes[i], allSeedNodes[j] = allSeedNodes[j], allSeedNodes[i]
	})

	// Exclude the current node's address from the list
	for _, nodeAddr := range allSeedNodes {
		if nodeAddr != leaderAddr {
			otherNodes = append(otherNodes, nodeAddr)
			return otherNodes
		}
	}

	// Add the seed node address to the list
	otherNodes = append(otherNodes, "seed.suatokens.com:8077")

	return otherNodes
}

// getNodeList retrieves the list of nodes from the database
func getNodesList() []byte {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of nodes from MongoDB
	cur, err := nodes.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching nodes from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode nodes from the cursor
	localNodesList := make(map[string]Node)
	for cur.Next(context.Background()) {
		var node Node
		err := cur.Decode(&node)
		if err != nil {
			log.Printf("Error decoding node from MongoDB: %v", err)
			return nil
		}
		localNodesList[node.Address] = node
	}

	// Convert the token list to JSON
	jsonNodesList, err := json.Marshal(localNodesList)
	if err != nil {
		log.Printf("Error encoding token list to JSON: %v", err)
		return nil
	}

	return jsonNodesList
}

// getTokenList retrieves the list of tokens from the database
func getTokenList() []byte {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of tokens from MongoDB
	cur, err := tokens.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching tokens from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode tokens from the cursor
	localTokenList := make(map[string]Token)
	for cur.Next(context.Background()) {
		var token Token
		err := cur.Decode(&token)
		if err != nil {
			log.Printf("Error decoding token from MongoDB: %v", err)
			return nil
		}
		localTokenList[token.ID] = token
	}

	// Convert the token list to JSON
	jsonTokenList, err := json.Marshal(localTokenList)
	if err != nil {
		log.Printf("Error encoding token list to JSON: %v", err)
		return nil
	}

	return jsonTokenList
}

// getMintList retrieves the list of mints from the database
func getMintList() []byte {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of mints from MongoDB
	cur, err := mints.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching mints from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode mints from the cursor
	localMintList := make(map[string]Mint)
	for cur.Next(context.Background()) {
		var mint Mint
		err := cur.Decode(&mint)
		if err != nil {
			log.Printf("Error decoding mint from MongoDB: %v", err)
			return nil
		}
		localMintList[mint.ID] = mint
	}

	// Convert the mint list to JSON
	jsonMintList, err := json.Marshal(localMintList)
	if err != nil {
		log.Printf("Error encoding mint list to JSON: %v", err)
		return nil
	}

	return jsonMintList
}

// getTransferList retrieves the list of transfers from the database
func getTransferList() []byte {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of transfers from MongoDB
	cur, err := transfers.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching transfers from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode transfers from the cursor
	localTransferList := make(map[string]Transfer)
	for cur.Next(context.Background()) {
		var transfer Transfer
		err := cur.Decode(&transfer)
		if err != nil {
			log.Printf("Error decoding transfer from MongoDB: %v", err)
			return nil
		}
		localTransferList[transfer.ID] = transfer
	}

	// Convert the transfer list to JSON
	jsonTransferList, err := json.Marshal(localTransferList)
	if err != nil {
		log.Printf("Error encoding transfer list to JSON: %v", err)
		return nil
	}

	return jsonTransferList
}

// getBalanceList retrieves the list of balances from the database
func getBalanceList() []byte {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of balances from MongoDB
	cur, err := balances.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching balances from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode balances from the cursor
	localBalanceList := make(map[string]Balance)
	for cur.Next(context.Background()) {
		var balance Balance
		err := cur.Decode(&balance)
		if err != nil {
			log.Printf("Error decoding balance from MongoDB: %v", err)
			return nil
		}
		localBalanceList[balance.Address] = balance
	}

	// Convert the transfer list to JSON
	jsonBalanceList, err := json.Marshal(localBalanceList)
	if err != nil {
		log.Printf("Error encoding balance list to JSON: %v", err)
		return nil
	}

	return jsonBalanceList
}

// getRandomNodeAddresses returns a random subset of node addresses
func getRandomNodeAddresses(allNodes map[string]Node, count int) []string {
	mutex.Lock()
	defer mutex.Unlock()

	// Extract addresses from the node map
	var addresses []string
	for address := range allNodes {
		addresses = append(addresses, address)
	}

	// Shuffle the addresses randomly
	rand.Shuffle(len(addresses), func(i, j int) {
		addresses[i], addresses[j] = addresses[j], addresses[i]
	})

	// Select the first 'count' addresses as the random subset
	if count > len(addresses) {
		count = len(addresses)
	}

	return addresses[:count]
}

// syncNodesList synchronizes the token list with other nodes
func syncNodesList() {

	// Fetch the list of nodes from MongoDB
	localNodesList := getNodesList()

	// Decode the JSON-encoded byte slice to get the map of nodes
	var localNodesMap map[string]Node
	err := json.Unmarshal(localNodesList, &localNodesMap)
	if err != nil {
		log.Printf("Error decoding local nodes list: %v", err)
		return
	}

	// Get a random subset of node addresses
	otherNodes := getRandomNodeAddresses(localNodesMap, 10)

	for _, nodeAddr := range otherNodes {

		if removeSpace(nodeAddr) == "" {
			log.Println("Empty node address, skipping sync.")
			continue
		}

		nodeAddr2 := strings.TrimPrefix(nodeAddr, "http://")
		// Send an HTTP request to the other node to sync the token list
		resp, err := http.Post("http://"+nodeAddr2+"/sync-nodes", "application/json", bytes.NewBuffer(localNodesList))
		if err != nil {
			log.Printf("Error syncing nodes list with %s: %v", nodeAddr, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("Failed to sync nodes list with %s. Status: %d", nodeAddr, resp.StatusCode)
			continue
		}

		// Decode the received node list from the other node
		var receivedNodesList map[string]Node
		err = json.NewDecoder(resp.Body).Decode(&receivedNodesList)
		if err != nil {
			log.Printf("Error decoding nodes list from %s: %v", nodeAddr, err)
			continue
		}

		// Save the received node list locally in the database
		for address, node := range receivedNodesList {
			registerNode(address)
			log.Printf("updating node in MongoDB: %v", node.LastSync)
		}

		log.Printf("Nodes list synced with %s", nodeAddr)
	}
}

// syncTokenList synchronizes the token list with other nodes
func syncTokenList() {

	// Fetch the list of tokens from MongoDB
	localTokenList := getTokenList()

	// Fetch the list of nodes from MongoDB
	localNodesList := getNodesList()

	// Decode the JSON-encoded byte slice to get the map of nodes
	var localNodesMap map[string]Node
	err := json.Unmarshal(localNodesList, &localNodesMap)
	if err != nil {
		log.Printf("Error decoding local nodes list: %v", err)
		return
	}

	// Get a random subset of node addresses
	otherNodes := getRandomNodeAddresses(localNodesMap, 10)

	for _, nodeAddr := range otherNodes {

		if removeSpace(nodeAddr) == "" {
			log.Println("Empty node address, skipping sync.")
			continue
		} else {
			nodeAddr2 := strings.TrimPrefix(nodeAddr, "http://")
			// Send an HTTP request to the other node to sync the token list
			resp, err := http.Post("http://"+nodeAddr2+"/sync-tokens", "application/json", bytes.NewBuffer(localTokenList))
			if err != nil {
				log.Printf("Error syncing token list with %s: %v", nodeAddr, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to sync token list with %s. Status: %d", nodeAddr, resp.StatusCode)
				continue
			}

			// Decode the received token list from the other node
			var receivedTokenList map[string]Token
			err = json.NewDecoder(resp.Body).Decode(&receivedTokenList)
			if err != nil {
				log.Printf("Error decoding token list from %s: %v", nodeAddr, err)
				continue
			}

			// Save the received token list locally in the database
			for id, token := range receivedTokenList {
				// Verify token hash
				isValidHash := verifyTokenHash(token)
				if !isValidHash {
					log.Printf("Received token with invalid hash from %s", nodeAddr)
					continue
				}

				// Verify signature
				isValidSignature := verifySignatureWithRPC(token.Minter, token.Signature, token.Hash)
				if !isValidSignature {
					log.Printf("Received token with invalid signature from %s", nodeAddr)
					continue
				}

				registerToken(id, token)
			}

			log.Printf("Token list synced with %s", nodeAddr)
		}

	}
}

// getMinterAddress retrieves the Minter address for a given Token ID
func getMinterAddress(tokenID string) (string, error) {
	// Define filter to find the token by its ID
	filter := bson.M{"id": tokenID}

	// Fetch the token from MongoDB
	var token Token
	err := tokens.FindOne(context.Background(), filter).Decode(&token)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// Token not found
			return "", errors.New("token not found")
		}
		// Other error occurred
		log.Printf("Error retrieving token from MongoDB: %v", err)
		return "", err
	}

	// Return the Minter address
	return token.Minter, nil
}

// GreaterThan checks if a Decimal128 value is greater than another Decimal128 value
func GreaterThan(a, b primitive.Decimal128) bool {
	// Convert Decimal128 values to strings
	strA := a.String()
	strB := b.String()

	// Convert strings to float64 for comparison
	floatA, err := strconv.ParseFloat(strA, 64)
	if err != nil {
		// Handle error
		return false
	}
	floatB, err := strconv.ParseFloat(strB, 64)
	if err != nil {
		// Handle error
		return false
	}

	// Compare float64 values
	return floatA > floatB
}

// isValidMintQuantity checks if the minted quantity is valid for the given token
func isValidMintQuantity(tokenID string, mintedQuantity primitive.Decimal128) (bool, error) {
	// Retrieve the token from the database
	var token Token
	err := tokens.FindOne(context.Background(), bson.M{"id": tokenID}).Decode(&token)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return false, errors.New("token not found")
		}
		return false, err
	}

	// Check if the token is mintable
	if !token.Mintable {
		return false, errors.New("token is not mintable")
	}

	// Calculate the maximum allowed quantity
	maxQuantity := token.Quantity

	// Check if the minted quantity exceeds the maximum allowed quantity
	if GreaterThan(mintedQuantity, maxQuantity) {
		return false, nil
	}

	// Minted quantity is valid
	return true, nil
}

// syncTokenList synchronizes the token list with other nodes
func syncMintList() {

	// Fetch the list of tokens from MongoDB
	localMintList := getMintList()

	// Fetch the list of nodes from MongoDB
	localNodesList := getNodesList()

	// Decode the JSON-encoded byte slice to get the map of nodes
	var localNodesMap map[string]Node
	err := json.Unmarshal(localNodesList, &localNodesMap)
	if err != nil {
		log.Printf("Error decoding local nodes list: %v", err)
		return
	}

	// Get a random subset of node addresses
	otherNodes := getRandomNodeAddresses(localNodesMap, 10)

	for _, nodeAddr := range otherNodes {

		if removeSpace(nodeAddr) == "" {
			log.Println("Empty node address, skipping sync.")
			continue
		} else {
			nodeAddr2 := strings.TrimPrefix(nodeAddr, "http://")
			// Send an HTTP request to the other node to sync the mint list
			resp, err := http.Post("http://"+nodeAddr2+"/sync-mints", "application/json", bytes.NewBuffer(localMintList))
			if err != nil {
				log.Printf("Error syncing mint list with %s: %v", nodeAddr, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to sync mint list with %s. Status: %d", nodeAddr, resp.StatusCode)
				continue
			}

			// Decode the received mint list from the other node
			var receivedMintList map[string]Mint
			err = json.NewDecoder(resp.Body).Decode(&receivedMintList)
			if err != nil {
				log.Printf("Error decoding mint list from %s: %v", nodeAddr, err)
				continue
			}

			// Save the received mint list locally in the database
			for id, mint := range receivedMintList {

				// Verify token existence
				if !tokenExistsLocally(mint.Token) {
					log.Printf("Received mint with non-existent token %s from %s", mint.Token, nodeAddr)
					continue
				}

				// Verify mint hash
				isValidHash := verifyMintHash(mint)
				if !isValidHash {
					log.Printf("Received mint with invalid hash from %s", nodeAddr)
					continue
				}

				minterAddress, err := getMinterAddress(mint.Token)
				if err != nil {
					log.Printf("Error getting Minter address: %v", err)
					// Handle error
					log.Printf("Received mint with invalid minter from %v", err)
					continue
				}

				isValidSignature := verifySignatureWithRPC(minterAddress, mint.Signature, mint.Hash)
				if !isValidSignature {
					log.Printf("Received mint with invalid signature from %s", nodeAddr)
					continue
				}

				// Verify mint quantity
				isValid, err := isValidMintQuantity(mint.Token, mint.Quantity)
				if err != nil {
					log.Printf("Error validating mint quantity: %v", err)
					// Handle error
					continue
				} else {
					if !isValid {
						// Minted quantity is invalid
						log.Printf("Received mint with invalid quantity from %s", nodeAddr)
						continue
					}
				}

				registerMint(id, mint)
			}

			log.Printf("Mint list synced with %s", nodeAddr)
		}

	}
}

// syncTransferList synchronizes the transfer list with other nodes
func syncTransferList() {

	// Fetch the list of transfers from MongoDB
	localTransferList := getTransferList()

	// Fetch the list of nodes from MongoDB
	localNodesList := getNodesList()

	// Decode the JSON-encoded byte slice to get the map of nodes
	var localNodesMap map[string]Node
	err := json.Unmarshal(localNodesList, &localNodesMap)
	if err != nil {
		log.Printf("Error decoding local nodes list: %v", err)
		return
	}

	// Get a random subset of node addresses
	otherNodes := getRandomNodeAddresses(localNodesMap, 10)

	for _, nodeAddr := range otherNodes {

		if removeSpace(nodeAddr) == "" {
			log.Println("Empty node address, skipping sync.")
			continue
		} else {
			nodeAddr2 := strings.TrimPrefix(nodeAddr, "http://")
			// Send an HTTP request to the other node to sync the transfer list
			resp, err := http.Post("http://"+nodeAddr2+"/sync-transfers", "application/json", bytes.NewBuffer(localTransferList))
			if err != nil {
				log.Printf("Error syncing transfer list with %s: %v", nodeAddr, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to sync transfer list with %s. Status: %d", nodeAddr, resp.StatusCode)
				continue
			}

			// Decode the received transfer list from the other node
			var receivedTransferList map[string]Transfer
			err = json.NewDecoder(resp.Body).Decode(&receivedTransferList)
			if err != nil {
				log.Printf("Error decoding transfer list from %s: %v", nodeAddr, err)
				continue
			}

			// Save the received transfer list locally in the database
			for id, transfer := range receivedTransferList {
				if isValidTransfer(transfer) {
					registerTransfer(id, transfer)
				} else {
					log.Printf("Received invalid transfer with ID %s from %s", id, nodeAddr)
				}
			}

			log.Printf("Transfer list synced with %s", nodeAddr)
		}

	}
}

// LessThanOrEqual checks if decimal `a` is less than or equal to decimal `b`
func LessThanOrEqual(a, b primitive.Decimal128) bool {
	strA := a.String()
	strB := b.String()
	return strA <= strB
}

// isValidTransfer checks if a transfer is valid
func isValidTransfer(transfer Transfer) bool {

	if LessThanOrEqual(transfer.Amount, primitive.NewDecimal128(0, 0)) {
		return false
	}

	// Check if the block height is provided and valid
	config := LoadConfig()
	if transfer.Block != "" {
		blockHash := transfer.Block

		// Call suacoin RPC to get block height from the given block hash
		blockHeight, err := getBlockHeight(blockHash)
		if err != nil {
			log.Printf("Error getting block height: %v", err)
			return false
		}

		// Get the block count from the Suacoin RPC server
		currentBlockHeight, err := GetBlockCount(config)
		if err != nil {
			log.Printf("Error getting current block height: %v", err)
			return false
		}

		// Check if the provided block hash corresponds to a block height after the current block height
		if blockHeight < int64(currentBlockHeight) {
			return false
		}
	}

	// Verify the signature
	isValidSignature := verifySignatureWithRPC(transfer.From, transfer.Signature, transfer.Hash)
	if !isValidSignature {
		return false
	}

	// Check if the transfer hash is valid
	isValidHash := verifyTransferHash(transfer)
	if !isValidHash {
		return false
	}

	return true
}

// syncBalanceList synchronizes the balance list with other nodes
func syncBalanceList() {

	// Fetch the list of balances from MongoDB
	localBalanceList := getBalanceList()

	// Fetch the list of nodes from MongoDB
	localNodesList := getNodesList()

	// Decode the JSON-encoded byte slice to get the map of nodes
	var localNodesMap map[string]Node
	err := json.Unmarshal(localNodesList, &localNodesMap)
	if err != nil {
		log.Printf("Error decoding local nodes list: %v", err)
		return
	}

	// Get a random subset of node addresses
	otherNodes := getRandomNodeAddresses(localNodesMap, 10)

	for _, nodeAddr := range otherNodes {

		if removeSpace(nodeAddr) == "" {
			log.Println("Empty node address, skipping sync.")
			continue
		} else {
			nodeAddr2 := strings.TrimPrefix(nodeAddr, "http://")
			// Send an HTTP request to the other node to sync the balance list
			resp, err := http.Post("http://"+nodeAddr2+"/sync-balances", "application/json", bytes.NewBuffer(localBalanceList))
			if err != nil {
				log.Printf("Error syncing balance list with %s: %v", nodeAddr, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to sync balance list with %s. Status: %d", nodeAddr, resp.StatusCode)
				continue
			}

			// Decode the received balance list from the other node
			var receivedBalanceList map[string]Balance
			err = json.NewDecoder(resp.Body).Decode(&receivedBalanceList)
			if err != nil {
				log.Printf("Error decoding balance list from %s: %v", nodeAddr, err)
				continue
			}

			for _, balance := range receivedBalanceList {
				// Check if the Address field is not empty
				if balance.Address != "" {
					registerBalance(balance.Address, balance)
				}
			}

			log.Printf("Balance list synced with %s", nodeAddr)
		}

	}
}

// Add a new handler function for handling the GET request with a token parameter
func getTokensList(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")

	if token == "" {
		http.Error(w, "Token parameter is missing", http.StatusBadRequest)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of tokens from MongoDB
	cur, err := tokens.Find(context.Background(), bson.M{"token": token})
	if err != nil {
		log.Printf("Error fetching tokens from MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode tokens from the cursor
	var tokenList []Token
	for cur.Next(context.Background()) {
		var token Token
		err := cur.Decode(&token)
		if err != nil {
			log.Printf("Error decoding token from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		tokenList = append(tokenList, token)
	}

	// Respond with the list of tokens in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenList)
}

func removeSpace(s string) string {
	rr := make([]rune, 0, len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			rr = append(rr, r)
		}
	}
	return string(rr)
}

// registerNode registers a new node or updates the last sync time of an existing node
func registerNode(address string) {

	if removeSpace(address) == "" {
		log.Println("Empty node address, skipping address.")
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Check if the node already exists in the database
	var existingNode Node
	err := nodes.FindOne(context.Background(), bson.M{"address": address}).Decode(&existingNode)
	if err == nil {
		// Node exists, update the last sync time
		_, err := nodes.UpdateOne(context.Background(), bson.M{"address": address}, bson.M{"$set": bson.M{"lastSync": time.Now()}})
		if err != nil {
			log.Printf("Error updating node in MongoDB: %v", err)
		}
	} else {
		// Node doesn't exist, insert a new node
		_, err := nodes.InsertOne(context.Background(), Node{Address: address, LastSync: time.Now()})
		if err != nil {
			log.Printf("Error inserting node into MongoDB: %v", err)
		}
	}
}

func listNodes(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of nodes from MongoDB
	cur, err := nodes.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching nodes from MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode nodes from the cursor
	nodeList = make(map[string]Node)
	for cur.Next(context.Background()) {
		var node Node
		err := cur.Decode(&node)
		if err != nil {
			log.Printf("Error decoding nodes from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		nodeList[node.Address] = node
	}

	// Respond with the list of tokens in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodeList)
}

// getNodeList retrieves the list of nodes from the database
func getNodeList() map[string]Node {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of nodes from MongoDB
	cur, err := nodes.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching nodes from MongoDB: %v", err)
		return nil
	}
	defer cur.Close(context.Background())

	// Decode nodes from the cursor
	nodeList = make(map[string]Node)
	for cur.Next(context.Background()) {
		var node Node
		err := cur.Decode(&node)
		if err != nil {
			log.Printf("Error decoding node from MongoDB: %v", err)
			return nil
		}
		nodeList[node.Address] = node
	}

	return nodeList
}

// syncNodeList synchronizes the node list with other nodes
func syncNodeList() {
	// Assuming you have a list of other node addresses
	otherNodes := getOtherNodeAddresses()

	for _, nodeAddr := range otherNodes {

		if removeSpace(nodeAddr) == "" {
			log.Println("Empty node address, skipping sync.")
			continue
		} else {

			nodeAddr2 := strings.TrimPrefix(nodeAddr, "http://")
			// Send an HTTP request to the other node to sync the node list
			resp, err := http.Post("http://"+nodeAddr2+"/sync-nodes", "application/json", nil)
			if err != nil {
				log.Printf("Error syncing node list with %s: %v", nodeAddr, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Failed to sync node list with %s. Status: %d", nodeAddr, resp.StatusCode)
				continue
			}

			log.Printf("Node list synced with %s", nodeAddr)
		}
	}
}

// syncNodesHandler handles requests from other nodes to sync the node list
func syncNodesHandler(w http.ResponseWriter, r *http.Request) {
	var receivedNodesList map[string]Node
	err := json.NewDecoder(r.Body).Decode(&receivedNodesList)
	if err != nil {
		log.Printf("Error decoding nodes list from request: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Save the received node list locally in the database
	for address, node := range receivedNodesList {
		if node.Address != "" {
			registerNode(address)
			log.Printf("updating node in MongoDB: %v", node.LastSync)
		}
	}

	// Fetch the list of nodes from MongoDB
	nodeList := getNodeList()

	log.Printf("Nodes list synced successfully.")
	// Respond with the list of nodes in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodeList)
}

// syncTokensHandler handles requests from other nodes to sync the token list
func syncTokensHandler(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Decode the received token list
	var receivedTokenList map[string]Token
	err := json.NewDecoder(r.Body).Decode(&receivedTokenList)
	if err != nil {
		log.Printf("Error decoding token list from request: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Save the received token list locally in the database
	for id, token := range receivedTokenList {

		zero := primitive.NewDecimal128(0, 0)

		amountResult, err := CompareDecimal128(token.Quantity, zero)
		if err != nil {
			// Handle the error
			break
		}

		if amountResult == -1 {
			fmt.Println("Quantity is less than or equal to 0.0")

		} else {
			// Check if the Address field is not empty
			if token.Minter != "" {
				registerToken(id, token)
			}
		}
	}

	// Respond with a custom success message
	log.Printf("Token list synced successfully.")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenList)
}

// syncMintsHandler handles requests from other nodes to sync the mint list
func syncMintsHandler(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Decode the received mint list
	var receivedMintList map[string]Mint
	err := json.NewDecoder(r.Body).Decode(&receivedMintList)
	if err != nil {
		log.Printf("Error decoding mint list from request: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Save the received mint list locally in the database
	for id, mint := range receivedMintList {

		zero := primitive.NewDecimal128(0, 0)

		amountResult, err := CompareDecimal128(mint.Amount, zero)
		if err != nil {
			// Handle the error
			break
		}

		if amountResult == -1 {
			fmt.Println("Amount is less than or equal to 0.0")

		} else {
			// Check if the Address field is not empty
			if mint.Payer != "" {
				registerMint(id, mint)
			}
		}
	}

	// Respond with a custom success message
	log.Printf("Mint list synced successfully.")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenList)
}

// syncTransfersHandler handles requests from other nodes to sync the transfer list
func syncTransfersHandler(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Decode the received transfer list
	var receivedTransferList map[string]Transfer
	err := json.NewDecoder(r.Body).Decode(&receivedTransferList)
	if err != nil {
		log.Printf("Error decoding transfer list from request: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Save the received transfer list locally in the database
	for id, transfer := range receivedTransferList {

		zero := primitive.NewDecimal128(0, 0)

		amountResult, err := CompareDecimal128(transfer.Amount, zero)
		if err != nil {
			// Handle the error
			break
		}

		if amountResult == -1 {
			fmt.Println("Amount is less than or equal to 0.0")

		} else {
			// Check if the Address field is not empty
			if transfer.To != "" {
				registerTransfer(id, transfer)
			}
		}
	}

	// Respond with a custom success message
	log.Printf("Transfer list synced successfully.")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenList)
}

// syncBalancesHandler handles requests from other nodes to sync the balance list
func syncBalancesHandler(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Decode the received balance list
	var receivedBalanceList map[string]Balance
	err := json.NewDecoder(r.Body).Decode(&receivedBalanceList)
	if err != nil {
		log.Printf("Error decoding balance list from request: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Save the received balance list locally in the database
	for _, balance := range receivedBalanceList {

		zero := primitive.NewDecimal128(0, 0)

		amountResult, err := CompareDecimal128(balance.Balance, zero)
		if err != nil {
			// Handle the error
			break
		}

		if amountResult == -1 {
			fmt.Println("Balance is less than or equal to 0.0")

		} else {
			// Check if the Address field is not empty
			if balance.Address != "" {
				registerBalance(balance.Address, balance)
			}
		}
	}

	// Respond with a custom success message
	log.Printf("Balance list synced successfully.")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenList)
}

// Add a new handler function for handling the GET request to list mints
func listMints(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of mints from MongoDB
	cur, err := mints.Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("Error fetching mints from MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode mints from the cursor
	var mintList []Mint
	for cur.Next(context.Background()) {
		var mint Mint
		err := cur.Decode(&mint)
		if err != nil {
			log.Printf("Error decoding mint from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		mintList = append(mintList, mint)
	}

	// Respond with the list of mints in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mintList)
}

func listMintsForToken(w http.ResponseWriter, r *http.Request) {
	// Parse the query parameter "token" from the request
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Bad Request: Missing 'token' parameter", http.StatusBadRequest)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Fetch the list of mints for the specified token from MongoDB
	cur, err := mints.Find(context.Background(), bson.M{"token": token})
	if err != nil {
		log.Printf("Error fetching mints for token from MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode mints from the cursor
	var mintList []Mint
	for cur.Next(context.Background()) {
		var mint Mint
		err := cur.Decode(&mint)
		if err != nil {
			log.Printf("Error decoding mint for token from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		mintList = append(mintList, mint)
	}

	// Respond with the list of mints for the specified token in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mintList)
}

// sendRPCRequest sends an HTTP POST request to the suacoin RPC server.
func sendRPCRequest(rpcNodeURL, rpcUser, rpcPass, rpcRequest string) ([]byte, error) {
	client := &http.Client{}

	req, err := http.NewRequest("POST", rpcNodeURL, strings.NewReader(rpcRequest))
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(rpcUser, rpcPass)
	req.Header.Set("Content-Type", "text/plain;")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func verifySignatureWithRPC(minterAddress, signature, message string) bool {
	// Get values from environment variables
	var rpcNodeURL = os.Getenv("suacoin_rpcnode_url")
	rpcUser := os.Getenv("suacoin_rpcuser")
	rpcPass := os.Getenv("suacoin_rpcpass")
	rpcNodeURL = strings.TrimPrefix(rpcNodeURL, "http://")
	rpcNodeURL = "http://" + rpcNodeURL

	// Build the RPC request to verify the signature
	rpcRequest := fmt.Sprintf(`{"jsonrpc": "1.0", "id": "suatokensnode", "method": "verifymessage", "params": ["%s", "%s", "%s"]}`, minterAddress, signature, message)

	// Send the RPC request
	response, err := sendRPCRequest(rpcNodeURL, rpcUser, rpcPass, rpcRequest)
	if err != nil {
		return false
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.Unmarshal(response, &result); err != nil {
		return false
	}

	// Check if the signature is valid
	isValid, ok := result["result"].(bool)
	if !ok {
		//return errors.New("failed to extract signature verification result from RPC response")
		return false
	}

	if !isValid {
		//return errors.New("signature verification failed")
		return false
	}

	return true
}

// Add a new handler function for handling the POST request to create a new mint
func createMint(w http.ResponseWriter, r *http.Request) {
	// Decode the request body into a Mint struct
	var newMint Mint
	err := json.NewDecoder(r.Body).Decode(&newMint)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Convert newTransfer.Amount to string
	amountStr := newMint.Amount.String()

	// Retrieve the number of decimals for the token
	decimals, err := getTokenDecimals(newMint.Token)
	if err != nil {
		decimals = 2
	}

	// Check if the string representation contains a decimal point
	if !strings.Contains(amountStr, ".") {
		if decimals > 0 && decimals <= 8 {
			amountStr += "." + strings.Repeat("0", decimals)
		}
	}

	numDecimals := countDecimals(amountStr)

	if numDecimals != decimals {
		http.Error(w, "Bad Request: Invalid amount", http.StatusBadRequest)
		return
	}

	// Convert newTransfer.Amount to primitive.Decimal128
	amount, err := primitive.ParseDecimal128(amountStr)
	if err != nil {
		http.Error(w, "Bad Request: Invalid amount", http.StatusBadRequest)
		return
	}
	newMint.Amount = amount

	// Now, you can use the CompareDecimal128 function to compare existingBalanceSender.Balance and newTransfer.Amount to 0.0
	zero := primitive.NewDecimal128(0, 0)

	amountResult, err := CompareDecimal128(newMint.Amount, zero)
	if err != nil {
		// Handle the error
		http.Error(w, "Bad Request: Invalid request body", http.StatusBadRequest)
		return
	}

	if amountResult == -1 {
		fmt.Println("amount is less than or equal to 0.0")
		// Handle the case where either balance or amount is less than or equal to 0.0
		http.Error(w, "Bad Request: amount is less than or equal to 0.0", http.StatusBadRequest)
		return
	}

	formattedPayer := newMint.Payer
	fmt.Printf("\"%s\"", formattedPayer)

	isMine, err := validatePayerAddress(formattedPayer)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	if isMine {
		fmt.Println("Payer Address is a mine address to this node")
	} else {
		fmt.Println("Payer Address is not a mine address to this node")
		return
	}

	// Check if tx already in Database exit and provide error
	var existingTxIdMint Mint
	err = mints.FindOne(context.Background(), bson.M{"amount": newMint.Amount,
		"token": newMint.Token,
		"block": newMint.Block}).Decode(&existingTxIdMint)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// No existing document found, continue processing
		} else {
			// An error occurred while querying the database, handle it accordingly
			// continue processing
		}
	} else {
		// Transaction already exists in the database, provide an error and exit
		fmt.Println("Mint already exists in the blockchain")
		http.Error(w, "Mint already exists in the blockchain", http.StatusUnauthorized)
		return
	}

	config := LoadConfig()

	// Check if block height is provided and valid
	if newMint.Block != "" {
		blockHash := newMint.Block

		// Call suacoin RPC to get block height from the given block hash
		blockHeight, err := getBlockHeight(blockHash)
		if err != nil {
			log.Printf("Error getting block height: %v", err)
			http.Error(w, "Error getting block height", http.StatusInternalServerError)
			return
		}

		config := LoadConfig()

		// Get the block count from the Suacoin RPC server
		currentBlockHeight, err := GetBlockCount(config)
		if err != nil {
			log.Printf("Error getting current block height: %v", err)
			http.Error(w, "Error getting current block height", http.StatusInternalServerError)
			return
		}

		// Check if the provided block hash corresponds to a block height before the current block height
		if blockHeight > int64(currentBlockHeight) {
			http.Error(w, "Block hash corresponds to a block after the current block height", http.StatusBadRequest)
			return
		}
	}

	// Fetch the list of tokens from MongoDB
	cur, err := tokens.Find(context.Background(), bson.M{"id": newMint.Token})
	if err != nil {
		log.Printf("Error fetching tokens from MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode tokens from the cursor
	var tokenList []Token
	for cur.Next(context.Background()) {
		var token Token
		err := cur.Decode(&token)
		if err != nil {
			log.Printf("Error decoding token from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		tokenList = append(tokenList, token)
	}

	var value primitive.Decimal128

	// Checking if NFT
	value, err = primitive.ParseDecimal128("1.00")
	if err != nil {
		fmt.Println("Error parsing Decimal128:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if value.String() == tokenList[0].Quantity.String() {
		fmt.Println("The Token is an NFT")
		http.Error(w, "The Token is an NFT", http.StatusUnauthorized)
		return
	}

	if tokenList[0].Mintable != true {
		http.Error(w, "Error Token not mintable", http.StatusUnauthorized)
		return
	}

	// Verify the signature
	err1 := verifySignatureWithRPC(tokenList[0].Minter, newMint.Signature, newMint.Hash)
	if err1 == false {
		http.Error(w, "Signature verification failed", http.StatusUnauthorized)
		return
	}

	isValid := verifyMintHash(newMint)
	fmt.Println("Is Mint Hash Valid?", isValid)

	if isValid == false {
		http.Error(w, "Hash verification failed", http.StatusUnauthorized)
		return
	}

	confirmations := 6

	// Get the account for the minter address
	accountLabel, err := getAccountForAddress(newMint.Payer)
	if err != nil {
		fmt.Println("Error getting account:", err)
		return
	}

	fmt.Printf("Account for address %s: %s\n", newMint.Payer, accountLabel)

	// Check account balance
	balance, err := checkAccountBalance(accountLabel, confirmations)
	if err != nil {
		fmt.Println("Error checking account balance:", err)
		http.Error(w, "Error checking account balance", http.StatusUnauthorized)
		return
	}

	// Get the average transaction fee
	avgFee, err := GetGasFee(config)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	tokenMintCost, err := strconv.ParseFloat("50.00", 64)
	if err != nil {
		fmt.Println("Error parsing float:", err)
		return
	}

	tokenMintCostRequired := tokenMintCost + avgFee

	if balance >= tokenMintCostRequired {

		err := setTransactionFee(avgFee)
		if err != nil {
			log.Println("Error setting transaction fee:", err)
			return
		}

		mintTokenString := fmt.Sprintf("mintToken %s, %s, %s, %s, %s",
			newMint.Token,
			newMint.Block,
			newMint.Amount.String(),
			newMint.Signature,
		)

		txid, err1 := sendTransaction(accountLabel, "17adZQgdejjLrNkFQaLKmgpSAmuRNyVH3x", tokenMintCost, mintTokenString)
		if err1 != nil {
			fmt.Println("Error sending transaction:", err1)
			return
		}

		fmt.Println("Transaction sent successfully")
		newMint.ID = txid

	} else {
		fmt.Println("Insufficient balance")
		http.Error(w, "Insufficient balance", http.StatusUnauthorized)
		return
	}

	newMint.Date = time.Now()
	originalQuantity := tokenList[0].Quantity.String()
	newamount := newMint.Amount.String()

	i, err := strconv.ParseFloat(originalQuantity, 64)
	if err != nil {
		panic(err)
	}

	j, err2 := strconv.ParseFloat(newamount, 64)
	if err2 != nil {
		panic(err)
	}

	k := i + j
	decimalValue := primitive.NewDecimal128(0, uint64(k))

	newMint.Quantity = decimalValue
	newMint.Previous = tokenList[0].Quantity
	newMint.Status = true

	mutex.Lock()
	defer mutex.Unlock()

	// Insert the new mint into MongoDB
	_, err = mints.InsertOne(context.Background(), newMint)
	if err != nil {
		log.Printf("Error inserting mint into MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Check if the balance exists for the specified address and token
	var existingBalance Balance
	err = balances.FindOne(context.Background(), bson.M{"address": tokenList[0].Minter, "token": tokenList[0].ID}).Decode(&existingBalance)
	if err != nil {
		http.Error(w, "Balance not found", http.StatusNotFound)
		return
	}

	x, err := strconv.ParseFloat(existingBalance.Balance.String(), 64)
	if err != nil {
		panic(err)
	}

	z := x + j
	// Convert z to string
	tStr := fmt.Sprintf("%.2f", z)

	decimalValue2, err := primitive.ParseDecimal128(tStr)
	if err != nil {
		log.Printf("Error converting float to NumberDecimal: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Convert j to string
	tStr2 := fmt.Sprintf("%.2f", j)

	decimalValue4, err := primitive.ParseDecimal128(tStr2)
	if err != nil {
		log.Printf("Error converting float to NumberDecimal: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	updatedBalance := Balance{
		Address:  tokenList[0].Minter,
		Txid:     newMint.ID,
		Token:    tokenList[0].ID,
		Quantity: decimalValue4,
		Balance:  decimalValue2,
		Status:   true,
		Block:    newMint.Block,
		Date:     time.Now(),
		LastSync: time.Now(),
	}

	// Update the balance in MongoDB
	_, err = tokens.ReplaceOne(context.Background(), bson.M{"token": tokenList[0].ID, "address": tokenList[0].Minter}, updatedBalance)
	if err != nil {
		log.Printf("Error updating balance in MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Respond with success message
	json.NewEncoder(w).Encode(fmt.Sprintf("Mint with ID %s created", newMint.ID))
}

func listTransfersForToken(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Extract the token ID from the query parameters
	tokenID := r.URL.Query().Get("token")
	if tokenID == "" {
		http.Error(w, "Token ID is required in the query parameters", http.StatusBadRequest)
		return
	}

	// Fetch the list of transfers for the specified token from MongoDB
	cur, err := transfers.Find(context.Background(), bson.M{"token": tokenID})
	if err != nil {
		log.Printf("Error fetching transfers for token %s from MongoDB: %v", tokenID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode transfers from the cursor
	var transferList []Transfer
	for cur.Next(context.Background()) {
		var transfer Transfer
		err := cur.Decode(&transfer)
		if err != nil {
			log.Printf("Error decoding transfer from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		transferList = append(transferList, transfer)
	}

	// Respond with the list of transfers for the specified token in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transferList)
}

func listTransfersForTokenAndAddress(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()

	// Extract the token ID and address from the query parameters
	tokenID := r.URL.Query().Get("token")
	address := r.URL.Query().Get("address")

	if tokenID == "" {
		http.Error(w, "Token ID is required in the query parameters", http.StatusBadRequest)
		return
	}

	// Define the filter criteria based on token and address
	filter := bson.M{"token": tokenID}
	if address != "" {
		filter["to"] = address
	}

	// Fetch the list of transfers for the specified token and address from MongoDB
	cur, err := transfers.Find(context.Background(), filter)
	if err != nil {
		log.Printf("Error fetching transfers for token %s and address %s from MongoDB: %v", tokenID, address, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(context.Background())

	// Decode transfers from the cursor
	var transferList []Transfer
	for cur.Next(context.Background()) {
		var transfer Transfer
		err := cur.Decode(&transfer)
		if err != nil {
			log.Printf("Error decoding transfer from MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		transferList = append(transferList, transfer)
	}

	// Respond with the list of transfers for the specified token and address in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transferList)
}

func CompareDecimal128(d1, d2 primitive.Decimal128) (int, error) {
	b1, exp1, err := d1.BigInt()
	if err != nil {
		return 0, err
	}
	b2, exp2, err := d2.BigInt()
	if err != nil {
		return 0, err
	}

	// Convert exp1 and exp2 to int64
	exp1Int64 := int64(exp1)
	exp2Int64 := int64(exp2)

	// Adjust the exponents to be equal
	if exp1Int64 < exp2Int64 {
		b1 = b1.Mul(b1, big.NewInt(10).Exp(big.NewInt(10), big.NewInt(exp2Int64-exp1Int64), nil))
	} else if exp2Int64 < exp1Int64 {
		b2 = b2.Mul(b2, big.NewInt(10).Exp(big.NewInt(10), big.NewInt(exp1Int64-exp2Int64), nil))
	}

	// Compare the adjusted BigInt values
	return b1.Cmp(b2), nil
}

func countDecimals(amountStr string) int {
	// Find the position of the decimal point
	decimalIndex := strings.Index(amountStr, ".")
	if decimalIndex == -1 {
		// No decimal point found, return 0
		return 0
	}

	// Calculate the number of decimals by counting the characters after the decimal point
	return len(amountStr) - decimalIndex - 1
}

func createTransfer(w http.ResponseWriter, r *http.Request) {
	// Decode the request body into a Transfer struct
	var newTransfer Transfer
	err := json.NewDecoder(r.Body).Decode(&newTransfer)
	if err != nil {
		http.Error(w, "Bad Request: Invalid request body", http.StatusBadRequest)
		return
	}

	// Convert newTransfer.Amount to string
	amountStr := newTransfer.Amount.String()

	decimals, err := getTokenDecimals(newTransfer.Token)
	if err != nil {
		decimals = 2
	}

	// Check if the string representation contains a decimal point
	if !strings.Contains(amountStr, ".") {
		if decimals > 0 && decimals <= 8 {
			amountStr += "." + strings.Repeat("0", decimals)
		}
	}

	numDecimals := countDecimals(amountStr)

	if numDecimals != decimals {
		http.Error(w, "Bad Request: Invalid amount", http.StatusBadRequest)
		return
	}

	// Convert newTransfer.Amount to primitive.Decimal128
	amount, err := primitive.ParseDecimal128(amountStr)
	if err != nil {
		http.Error(w, "Bad Request: Invalid amount", http.StatusBadRequest)
		return
	}
	newTransfer.Amount = amount

	var existingBalanceSender Balance
	err = balances.FindOne(context.Background(), bson.M{"address": newTransfer.From, "token": newTransfer.Token}).Decode(&existingBalanceSender)
	if err != nil {
		// If balance does not exist for receiver
		if err == mongo.ErrNoDocuments {
			// Continue
		} else {
			// Other error occurred
			http.Error(w, "Error retrieving balance for sender", http.StatusInternalServerError)
			return
		}
	}

	// Now, you can use the CompareDecimal128 function to compare existingBalanceSender.Balance and newTransfer.Amount to 0.0
	zero := primitive.NewDecimal128(0, 0)

	// Check if both existingBalanceSender.Balance and newTransfer.Amount are greater than 0.0
	balanceResult, err := CompareDecimal128(existingBalanceSender.Balance, zero)
	if err != nil {
		// Handle the error
		http.Error(w, "Bad Request: Invalid request body", http.StatusBadRequest)
		return
	}

	amountResult, err := CompareDecimal128(newTransfer.Amount, zero)
	if err != nil {
		// Handle the error
		http.Error(w, "Bad Request: Invalid request body", http.StatusBadRequest)
		return
	}

	if balanceResult == -1 || amountResult == -1 {
		fmt.Println("Balance or amount is less than or equal to 0.0")
		// Handle the case where either balance or amount is less than or equal to 0.0
		http.Error(w, "Bad Request: Balance or amount is less than or equal to 0.0", http.StatusBadRequest)
		return
	}

	// Now compare the balance and the amount
	result, err := CompareDecimal128(existingBalanceSender.Balance, amount)
	if err != nil {
		http.Error(w, "Bad Request: Invalid request body", http.StatusBadRequest)
		return
	}

	if result == -1 {
		// newTransfer.Amount is greater than existingBalanceSender.Balance
		fmt.Println("Insufficient balance")
		http.Error(w, "Insufficient balance", http.StatusUnauthorized)
		return
	}

	fmt.Println("balance: ", existingBalanceSender.Balance)
	fmt.Println("amount: ", amount)

	formattedPayer := newTransfer.Payer
	fmt.Printf("\"%s\"", formattedPayer)

	isMine, err := validatePayerAddress(formattedPayer)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	if isMine {
		fmt.Println("Payer Address is a mine address to this node")
	} else {
		fmt.Println("Payer Address is not a mine address to this node")
		return
	}

	config := LoadConfig()

	// Check if block height is provided and valid
	if newTransfer.Block != "" {
		blockHash := newTransfer.Block

		// Call suacoin RPC to get block height from the given block hash
		blockHeight, err := getBlockHeight(blockHash)
		if err != nil {
			log.Printf("Error getting block height: %v", err)
			http.Error(w, "Error getting block height", http.StatusInternalServerError)
			return
		}

		// Get the block count from the Suacoin RPC server
		currentBlockHeight, err := GetBlockCount(config)
		if err != nil {
			log.Printf("Error getting current block height: %v", err)
			http.Error(w, "Error getting current block height", http.StatusInternalServerError)
			return
		}

		// Check if the provided block hash corresponds to a block height before the current block height
		if blockHeight > int64(currentBlockHeight) {
			http.Error(w, "Block hash corresponds to a block after the current block height", http.StatusBadRequest)
			return
		}
	}

	// Check if tx already in Database exit and provide error
	var existingTxIdBalance Balance
	err = balances.FindOne(context.Background(), bson.M{"address": newTransfer.To,
		"token":  newTransfer.Token,
		"amount": newTransfer.Amount,
		"block":  newTransfer.Block}).Decode(&existingTxIdBalance)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// No existing document found, continue processing
		} else {
			// An error occurred while querying the database, handle it accordingly
			// continue processing
		}
	} else {
		// Transaction already exists in the database, provide an error and exit
		fmt.Println("Transaction already exists in the blockchain")
		http.Error(w, "Transaction already exists in the blockchain", http.StatusUnauthorized)
		return
	}

	// Verify the signature
	err1 := verifySignatureWithRPC(newTransfer.From, newTransfer.Signature, newTransfer.Hash)
	if err1 == false {
		http.Error(w, "Signature verification failed", http.StatusUnauthorized)
		return
	}

	isValid := verifyTransferHash(newTransfer)
	fmt.Println("Is Transfer Hash Valid?", isValid)

	if isValid == false {
		http.Error(w, "Signature verification failed", http.StatusUnauthorized)
		return
	}

	confirmations := 6

	// Get the account for the payer address
	accountLabel, err := getAccountForAddress(newTransfer.Payer)
	if err != nil {
		fmt.Println("Error getting account:", err)
		return
	}

	fmt.Printf("Account for address %s: %s\n", newTransfer.Payer, accountLabel)

	// Check account balance
	balance, err := checkAccountBalance(accountLabel, confirmations)
	if err != nil {
		fmt.Println("Error checking account balance:", err)
		http.Error(w, "Error checking account balance", http.StatusUnauthorized)
		return
	}

	// Get the average transaction fee
	avgFee, err := GetGasFee(config)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	tokenTransferCost, err := strconv.ParseFloat("0.010000", 64)
	if err != nil {
		fmt.Println("Error parsing float:", err)
		return
	}

	tokenTransferCostRequired := tokenTransferCost + avgFee

	if balance >= tokenTransferCostRequired {

		err := setTransactionFee(avgFee)
		if err != nil {
			log.Println("Error setting transaction fee:", err)
			return
		}

		transferTokenString := fmt.Sprintf("transferToken %s, %s, %s, %s, %s",
			newTransfer.Token,
			newTransfer.From,
			newTransfer.Amount.String(),
			newTransfer.To,
			newTransfer.Signature,
		)

		txid, err1 := sendTransaction(accountLabel, "1GsDHnPCVD4Qt1WY46EgL9UhefuCx6WGRW", tokenTransferCost, transferTokenString)
		if err1 != nil {
			fmt.Println("Error sending transaction:", err1)
			return
		}

		fmt.Println("Transaction sent successfully")
		newTransfer.ID = txid

	} else {
		fmt.Println("Insufficient balance")
		http.Error(w, "Insufficient balance", http.StatusUnauthorized)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// Set the Date field to the current time
	newTransfer.Date = time.Now()
	newTransfer.Status = true

	// Insert the new transfer into MongoDB
	_, err = transfers.InsertOne(context.Background(), newTransfer)
	if err != nil {
		log.Printf("Error inserting transfer into MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	j, err := strconv.ParseFloat(newTransfer.Amount.String(), 64)
	if err != nil {
		panic(err)
	}

	// Check if the balance exists for the specified address and token for Receiver
	var existingBalance Balance
	err = balances.FindOne(context.Background(), bson.M{"address": newTransfer.To, "token": newTransfer.Token}).Decode(&existingBalance)
	if err != nil {
		// If balance does not exist for receiver, create a new entry
		if err == mongo.ErrNoDocuments {
			// Create new balance entry for receiver
			newReceiverBalance := Balance{
				Address:  newTransfer.To,
				Txid:     newTransfer.ID,
				Token:    newTransfer.Token,
				Quantity: newTransfer.Amount,
				Balance:  newTransfer.Amount,
				Status:   true,
				Block:    newTransfer.Block,
				Date:     time.Now(),
				LastSync: time.Now(),
			}
			_, err := balances.InsertOne(context.Background(), newReceiverBalance)
			if err != nil {
				log.Printf("Error creating new balance for receiver: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		} else {
			// Other error occurred
			http.Error(w, "Error retrieving balance for receiver", http.StatusInternalServerError)
			return
		}
	} else {
		// Update receiver's existing balance
		x, err := strconv.ParseFloat(existingBalance.Balance.String(), 64)
		if err != nil {
			panic(err)
		}
		z := x + j

		// Convert z to string
		tStr := fmt.Sprintf("%.2f", z)

		// Convert tStr to NumberDecimal
		decimalValue2, err := primitive.ParseDecimal128(tStr)
		if err != nil {
			log.Printf("Error converting float to NumberDecimal: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		updatedReceiverBalance := Balance{
			Address:  newTransfer.To,
			Txid:     newTransfer.ID,
			Token:    newTransfer.Token,
			Quantity: newTransfer.Amount,
			Balance:  decimalValue2,
			Status:   true,
			Block:    newTransfer.Block,
			Date:     time.Now(),
			LastSync: time.Now(),
		}

		_, err = balances.ReplaceOne(context.Background(), bson.M{"token": newTransfer.Token, "address": newTransfer.To}, updatedReceiverBalance)
		if err != nil {
			log.Printf("Error updating receiver's balance in MongoDB: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	// Check if the balance exists for the specified address and token for Sender
	var existingBalance2 Balance
	err = balances.FindOne(context.Background(), bson.M{"address": newTransfer.From, "token": newTransfer.Token}).Decode(&existingBalance2)
	if err != nil {
		http.Error(w, "Balance not found", http.StatusNotFound)
		return
	}

	// Deduct transfer amount from sender's balance
	k, err := strconv.ParseFloat(existingBalance2.Balance.String(), 64)
	if err != nil {
		panic(err)
	}
	t := k - j

	// Convert t to string
	tStr := fmt.Sprintf("%.2f", t)

	// Convert tStr to NumberDecimal
	decimalValue3, err := primitive.ParseDecimal128(tStr)
	if err != nil {
		log.Printf("Error converting float to NumberDecimal: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	updatedSenderBalance := Balance{
		Address:  newTransfer.From,
		Txid:     newTransfer.ID,
		Token:    newTransfer.Token,
		Quantity: newTransfer.Amount,
		Balance:  decimalValue3,
		Status:   true,
		Block:    newTransfer.Block,
		Date:     time.Now(),
		LastSync: time.Now(),
	}

	_, err = balances.ReplaceOne(context.Background(), bson.M{"token": newTransfer.Token, "address": newTransfer.From}, updatedSenderBalance)
	if err != nil {
		log.Printf("Error updating sender's balance in MongoDB: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Respond with success message
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fmt.Sprintf("Transfer with ID %s created", newTransfer.ID))
}

// handleFirstSync handles the first sync of a new node
func handleFirstSync() {
	// Contact seed.suatokens.com or any other seed node to download the initial list of nodes
	suatokensNodeURL := os.Getenv("suatokens_node_url")
	if suatokensNodeURL == "" {
		suatokensNodeURL = "seed.suatokens.com:8077"
	}

	resp, err := http.Get("http://" + suatokensNodeURL + "/node/list")
	if err != nil {
		log.Printf("Error contacting seed node: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to download node list from seed node. Status: %d", resp.StatusCode)
		return
	}

	// Decode the received node list
	var seedNodeList map[string]Node
	err = json.NewDecoder(resp.Body).Decode(&seedNodeList)
	if err != nil {
		log.Printf("Error decoding node list from seed node: %v", err)
		return
	}

	// Register the nodes from the seed node list
	for _, node := range seedNodeList {
		registerNode(node.Address)
	}

	registerNode(getExternalIP() + ":8077")

	// Sync the node list with other nodes
	syncNodeList()

	// Contact seed.suatokens.com to download the initial list of tokens
	resp, err = http.Get("http://" + suatokensNodeURL + "/token/list")
	if err != nil {
		log.Printf("Error contacting seed node for tokens: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to download token list from seed node. Status: %d", resp.StatusCode)
		return
	}

	// Decode the received token list
	var seedTokenList map[string]Token
	err = json.NewDecoder(resp.Body).Decode(&seedTokenList)
	if err != nil {
		log.Printf("Error decoding token list from seed node: %v", err)
		return
	}

	// Register the tokens from the seed token list
	for id, token := range seedTokenList {
		registerToken(id, token)
	}

	syncNodesList()
	syncTokenList()
	syncMintList()
	syncTransferList()
	syncBalanceList()

}

// registerToken registers a new token or updates an existing token
func registerToken(id string, token Token) {
	// Update the token in the database
	_, err := tokens.ReplaceOne(context.Background(), bson.M{"id": id}, token, options.Replace().SetUpsert(true))
	if err != nil {
		log.Printf("Error registering token in MongoDB: %v", err)
	}
}

// registerMint registers a new mint or updates an existing mint
func registerMint(id string, mint Mint) {
	// Update the mint in the database
	_, err := mints.ReplaceOne(context.Background(), bson.M{"id": id}, mint, options.Replace().SetUpsert(true))
	if err != nil {
		log.Printf("Error registering mint in MongoDB: %v", err)
	}
}

// GetAvgFeeHandler handles the GET request to /getavgfee endpoint
func GetAvgFeeHandler(w http.ResponseWriter, r *http.Request) {
	// Create a sample Configuration struct (replace with your actual configuration)
	config := LoadConfig()

	// Call the GetGasFee function to get the average fee
	avgFee, err := GetGasFee(config)
	if err != nil {
		http.Error(w, "Failed to retrieve average fee", http.StatusInternalServerError)
		return
	}

	// Create a JSON response
	response := map[string]float64{"average_fee": avgFee}
	jsonResponse, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "Failed to marshal JSON response", http.StatusInternalServerError)
		return
	}

	// Set the Content-Type header and write the response
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}

// registerTransfer registers a new transfer or updates an existing transfer
func registerTransfer(id string, transfer Transfer) {
	// Update the transfer in the database
	_, err := transfers.ReplaceOne(context.Background(), bson.M{"id": id}, transfer, options.Replace().SetUpsert(true))
	if err != nil {
		log.Printf("Error registering transfer in MongoDB: %v", err)
	}
}

// registerBalance registers a new balance or updates an existing balance
func registerBalance(address string, balance Balance) {
	// Update the balance in the database
	_, err := balances.ReplaceOne(context.Background(), bson.M{"address": address}, balance, options.Replace().SetUpsert(true))
	if err != nil {
		log.Printf("Error registering transfer in MongoDB: %v", err)
	}
}

// getExternalIP gets the external IP address of the current node
func getExternalIP() string {
	// Make a request to the IPify service
	resp, err := http.Get("https://api64.ipify.org?format=json")
	if err != nil {
		return "127.0.0.1"
	}
	defer resp.Body.Close()

	// Decode the JSON response
	var ipResponse IPResponse
	if err := json.NewDecoder(resp.Body).Decode(&ipResponse); err != nil {
		return "127.0.0.1"
	}

	return ipResponse.IP
}

func getBalance(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	tokenID := r.URL.Query().Get("token")
	address := r.URL.Query().Get("address")

	// Validate the request parameters
	if tokenID == "" || address == "" {
		http.Error(w, "Both 'token' and 'address' must be provided in the query parameters", http.StatusBadRequest)
		return
	}

	// Check if the token exists
	var existingToken Token
	err := tokens.FindOne(context.Background(), bson.M{"id": tokenID}).Decode(&existingToken)
	if err != nil {
		http.Error(w, "Token not found", http.StatusNotFound)
		return
	}

	// Check if the balance exists for the specified address and token
	var existingBalance Balance
	err = balances.FindOne(context.Background(), bson.M{"address": address, "token": existingToken.ID}).Decode(&existingBalance)
	if err != nil {
		http.Error(w, "Balance not found", http.StatusNotFound)
		return
	}

	// Respond with the existing balance in JSON format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(existingBalance)
}

func main() {

	// Load environment variables from .env file
	config := LoadConfig()

	// Set the Suacoin RPC node URL if not specified in .env
	if config.SuacoinRPCNodeURL == "" {
		config.SuacoinRPCNodeURL = "localhost:8442"
	}

	// Initialize MongoDB with the specified database name or use "tokensdb" if not specified
	databaseName := os.Getenv("databasename")
	config.DatabaseName = databaseName
	if databaseName == "" {
		databaseName = "tokensdb"
		config.DatabaseName = databaseName
	}
	initMongo(databaseName)

	// Start the first sync process in the background
	go handleFirstSync()

	// Start the leader election process in the background
	go leaderElection()

	// Register the default node if not specified in .env
	suatokensNodeURL := os.Getenv("suatokens_node_url")
	if suatokensNodeURL == "" {
		suatokensNodeURL = "seed.suatokens.com:8077"
	}
	registerNode(suatokensNodeURL)

	// Set up a ticker for syncing nodes and tokens every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Run a goroutine for periodic syncing
	go func() {
		for {
			select {
			case <-ticker.C:
				// Sync nodes and tokens every 5 minutes
				syncNodesList()
				syncTokenList()
				syncMintList()
				syncTransferList()
				syncBalanceList()
			}
		}
	}()

	// Set up HTTP server with Gorilla Mux
	router := mux.NewRouter()
	router.HandleFunc("/token/create", createToken).Methods("POST")
	router.HandleFunc("/token/list", listTokens).Methods("GET")
	router.HandleFunc("/token/list", getTokensList).Methods("GET")
	router.HandleFunc("/mints/create", createMint).Methods("POST")
	router.HandleFunc("/mints/list", listMints).Methods("GET")
	router.HandleFunc("/mints/list", listMintsForToken).Methods("GET").Queries("token", "{token}")
	router.HandleFunc("/transfers/create", createTransfer).Methods("POST")
	router.HandleFunc("/transfers/list", listTransfersForToken).Methods("GET").Queries("token", "{token}")
	router.HandleFunc("/transfers/list", listTransfersForTokenAndAddress).Methods("GET").
		Queries("token", "{token}", "address", "{address}")
	router.HandleFunc("/node/list", listNodes).Methods("GET")
	router.HandleFunc("/sync-tokens", syncTokensHandler).Methods("POST")
	router.HandleFunc("/sync-mints", syncMintsHandler).Methods("POST")
	router.HandleFunc("/sync-transfers", syncTransfersHandler).Methods("POST")
	router.HandleFunc("/sync-balances", syncBalancesHandler).Methods("POST")
	router.HandleFunc("/sync-nodes", syncNodesHandler).Methods("POST")
	router.HandleFunc("/balance", getBalance).Methods("GET")
	router.HandleFunc("/blockcount", BlockCountHandler).Methods("GET")
	router.HandleFunc("/getavgfee", GetAvgFeeHandler)

	// Run the HTTP server
	log.Fatal(http.ListenAndServe(":8077", router))
}
