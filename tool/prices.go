package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var coingeckoIDs = map[string]string{
	"BTC":   "bitcoin",
	"ETH":   "ethereum",
	"SOL":   "solana",
	"DOGE":  "dogecoin",
	"XRP":   "ripple",
	"ADA":   "cardano",
	"DOT":   "polkadot",
	"AVAX":  "avalanche-2",
	"MATIC": "polygon",
	"LINK":  "chainlink",
	"UNI":   "uniswap",
	"SHIB":  "shiba-inu",
	"SUI":   "sui",
	"PEPE":  "pepe",
	"LTC":   "litecoin",
	"BCH":   "bitcoin-cash",
	"FIL":   "filecoin",
	"ATOM":  "cosmos",
	"OP":    "optimism",
	"ARB":   "arbitrum",
	"NEAR":  "near",
	"AAVE":  "aave",
	"CRO":   "crypto-com-chain",
	"ALGO":  "algorand",
	"FTM":   "fantom",
	"INJ":   "injective-protocol",
	"TIA":   "celestia",
	"SEI":   "sei-network",
	"PENDLE": "pendle",
	"JUP":  "jupiter",
	"RUNE": "thorchain",
	"ETC":  "ethereum-classic",
	"XLM":  "stellar",
	"TRX":  "tron",
	"VET":  "vechain",
	"THETA": "theta-token",
	"FTT":  "ftx-token",
	"EGLD": "elrond-erd-2",
	"SAND": "the-sandbox",
	"MANA": "decentraland",
	"AXS":  "axie-infinity",
	"APE":  "apecoin",
	"GRT":  "the-graph",
	"ICP":  "internet-computer",
	"RNDR": "render-token",
	"FET":  "fetch-ai",
	"AGIX": "singularitynet",
	"OCEAN": "ocean-protocol",
	"CFX":  "conflux-token",
}

func isCrypto(symbol string) bool {
	_, ok := coingeckoIDs[strings.ToUpper(symbol)]
	return ok
}

type chartResponse struct {
	Chart *chartWrapper `json:"chart"`
}
type chartWrapper struct {
	Result []chartResult `json:"result"`
	Error  interface{}   `json:"error"`
}
type chartResult struct {
	Meta chartMeta `json:"meta"`
}
type chartMeta struct {
	RegularMarketPrice  float64 `json:"regularMarketPrice"`
	ChartPreviousClose  float64 `json:"chartPreviousClose"`
	Currency            string  `json:"currency"`
	ExchangeName        string  `json:"exchangeName"`
	ShortName           string  `json:"shortName"`
	InstrumentType      string  `json:"instrumentType"`
}

type quoteResponse struct {
	QuoteResponse quoteWrapper `json:"quoteResponse"`
}
type quoteWrapper struct {
	Result []quoteResult `json:"result"`
	Error  interface{}   `json:"error"`
}
type quoteResult struct {
	ShortName          string  `json:"shortName"`
	RegularMarketPrice float64 `json:"regularMarketPrice"`
	RegularMarketChangePercent float64 `json:"regularMarketChangePercent"`
	Currency           string  `json:"currency"`
	Exchange           string  `json:"exchange"`
	QuoteType          string  `json:"quoteType"`
	MarketState        string  `json:"marketState"`
}

type coingeckoResponse map[string]struct {
	Usd         float64 `json:"usd"`
	Usd24hChange float64 `json:"usd_24h_change"`
}

type GetPrice struct{}

func (t *GetPrice) Name() string        { return "get_price" }
func (t *GetPrice) Description() string { return "Get current price and 24h change for a stock or cryptocurrency. Provide a ticker symbol like 'AAPL', 'TSLA', 'GOOGL' for stocks, or 'BTC', 'ETH', 'SOL' for crypto." }
func (t *GetPrice) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"symbol": strParam("Ticker symbol. E.g. 'AAPL', 'TSLA', 'GOOGL' for stocks, 'BTC', 'ETH', 'SOL' for crypto."),
	}, []string{"symbol"})
}

func (t *GetPrice) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	symbol := strings.TrimSpace(strings.ToUpper(getStringArg(args, "symbol")))
	if symbol == "" {
		return "Please provide a ticker symbol (e.g., 'AAPL', 'BTC', 'ETH').", nil
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Try Yahoo Finance v7 quote endpoint (works for stocks and crypto with -USD)
	yahooSymbol := symbol
	if isCrypto(symbol) && !strings.HasSuffix(symbol, "-USD") {
		yahooSymbol = symbol + "-USD"
	}

	price, change, name, currency, err := tryYahooFinance(ctx, client, yahooSymbol)
	if err != nil {
		// Try CoinGecko as fallback for known crypto
		if id, ok := coingeckoIDs[symbol]; ok {
			price, change, name, err = tryCoinGecko(ctx, client, id)
			if err == nil {
				return formatPrice(symbol, price, change, name, "USD", "CoinGecko"), nil
			}
		}
		if isCrypto(symbol) {
			// Try Yahoo with -USD suffix if we didn't already
			if yahooSymbol == symbol {
				altSymbol := symbol + "-USD"
				price, change, name, currency, err2 := tryYahooFinance(ctx, client, altSymbol)
				if err2 == nil {
					return formatPrice(symbol, price, change, name, currency, "Yahoo Finance"), nil
				}
			}
		}
		return fmt.Sprintf("Could not fetch price for '%s'. Try a stock ticker like 'AAPL' or crypto like 'BTC'.", symbol), nil
	}

	return formatPrice(symbol, price, change, name, currency, "Yahoo Finance"), nil
}

func tryYahooFinance(ctx context.Context, client *http.Client, symbol string) (price float64, change float64, name string, currency string, err error) {
	// Try v7 quote endpoint first
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v7/finance/quote?symbols=%s", symbol)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var qr quoteResponse
		if json.Unmarshal(body, &qr) == nil && len(qr.QuoteResponse.Result) > 0 {
			r := qr.QuoteResponse.Result[0]
			return r.RegularMarketPrice, r.RegularMarketChangePercent, r.ShortName, r.Currency, nil
		}
	}

	// Fall back to v8 chart endpoint
	url = fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s", symbol)
	req, _ = http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err = client.Do(req)
	if err != nil {
		return 0, 0, "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var cr chartResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return 0, 0, "", "", err
	}
	if cr.Chart == nil || len(cr.Chart.Result) == 0 {
		return 0, 0, "", "", fmt.Errorf("no data")
	}
	m := cr.Chart.Result[0].Meta
	chg := 0.0
	if m.ChartPreviousClose > 0 {
		chg = (m.RegularMarketPrice - m.ChartPreviousClose) / m.ChartPreviousClose * 100
	}
	return m.RegularMarketPrice, chg, m.ShortName, m.Currency, nil
}

func tryCoinGecko(ctx context.Context, client *http.Client, id string) (price float64, change float64, name string, err error) {
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd&include_24hr_change=true", id)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var cg coingeckoResponse
	if err := json.Unmarshal(body, &cg); err != nil {
		return 0, 0, "", err
	}
	data, ok := cg[id]
	if !ok {
		return 0, 0, "", fmt.Errorf("no data")
	}
	name = strings.ReplaceAll(id, "-", " ")
	if len(name) > 0 {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return data.Usd, data.Usd24hChange, name, nil
}

func formatPrice(symbol string, price float64, change float64, name string, currency string, source string) string {
	chgChar := "📈"
	chgSign := "+"
	if change < 0 {
		chgChar = "📉"
		chgSign = ""
	}
	var nameLine string
	if name != "" {
		nameLine = fmt.Sprintf("**%s** (%s)\n", name, symbol)
	} else {
		nameLine = fmt.Sprintf("**%s**\n", symbol)
	}

	var currencyStr string
	switch currency {
	case "USD":
		currencyStr = "$"
	case "GBP":
		currencyStr = "£"
	case "EUR":
		currencyStr = "€"
	case "JPY":
		currencyStr = "¥"
	default:
		currencyStr = currency + " "
	}

	return fmt.Sprintf("%s%s%.2f %s %s%.2f%%\nAPI: %s\n⚠️ Not financial advice. Prices may be delayed or inaccurate.", nameLine, currencyStr, price, chgChar, chgSign, change, source)
}
