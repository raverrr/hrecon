package main

import (
        "bufio"
        "crypto/tls"
        "flag"
        "fmt"
        "io"
        "net/http"
        "net/url"
        "os"
        "regexp"
        "sort"
        "strings"
        "sync"
        "time"
)

const (
    Red    = "\033[31m"
    Green  = "\033[32m"
    Yellow = "\033[33m"
    Blue   = "\033[34m"
    Reset  = "\033[0m"
)

var (
        client        *http.Client
        wg            sync.WaitGroup
        totalRequests uint64
        totalErrors   uint64
        mu            sync.Mutex
        headerCount   map[string]int
)

var reverse bool
var quiet bool
var header string
var headervalue string
var ExtractedHeaderValue string
var headerPattern string
var method string
var feed int
var headerFrequency bool
var topRareHeaders int

func main() {
        // flag definitions
        flag.BoolVar(&reverse, "r", false, "Match anything other than the supplied pattern")
        flag.BoolVar(&quiet, "q", false, "Don't print the received header")
        flag.IntVar(&feed, "f", 50, "Delay the ingestion of URLs by X milliseconds")
        flag.StringVar(&header, "h", "", "Custom header")
        flag.StringVar(&method, "m", "GET", "Request method (GET or HEAD)")
        flag.StringVar(&ExtractedHeaderValue, "he", "", "Header value to extract")
        flag.StringVar(&headerPattern, "hp", "", "Header pattern to match")
        flag.StringVar(&headervalue, "hv", "", "Custom header value")
        flag.BoolVar(&headerFrequency, "hf", false, "Show header frequency statistics")
        flag.IntVar(&topRareHeaders, "trh", 10, "Number of top rare headers to display")

        flag.Parse()

        headerCount = make(map[string]int)

        input := os.Stdin

        if flag.NArg() > 0 {
                file, err := os.Open(flag.Arg(0))
                if err != nil {
                        fmt.Fprintf(os.Stderr, "Failed to open file: %s\n", err)
                        os.Exit(1)
                }
                defer file.Close()
                input = file
        }

        sc := bufio.NewScanner(input)

        client = &http.Client{
                Transport: &http.Transport{
                        MaxIdleConns:        20,
                        MaxIdleConnsPerHost: 20,
                        MaxConnsPerHost:     20,
                        TLSClientConfig: &tls.Config{
                                InsecureSkipVerify: true,
                        },
                },
                Timeout: 5 * time.Second,
                CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
                        return http.ErrUseLastResponse
                },
        }

        semaphore := make(chan bool, 20)

        for sc.Scan() {
                raw := sc.Text()
                time.Sleep(time.Duration(feed) * time.Millisecond)
                wg.Add(1)
                semaphore <- true
                go func(raw string) {
                        defer wg.Done()
                        u, err := url.ParseRequestURI(raw)
                        if err != nil {
                                mu.Lock()
                                totalErrors++
                                mu.Unlock()
                                return
                        }
                        fetchURL(u)
                }(raw)
                <-semaphore
        }

        wg.Wait()
        printCounters()

        if sc.Err() != nil {
                fmt.Fprintf(os.Stderr, "\nError: %s\n", sc.Err())
        }

        if headerFrequency {
                displayHeaderFrequency()
        }
}

func fetchURL(u *url.URL) {
        req, err := http.NewRequest(method, u.String(), nil)
        if err != nil {
                mu.Lock()
                totalErrors++
                mu.Unlock()
                return
        }
        req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/96.0.4664.110 Safari/537.36")
        if !containsEmpty(header) && !containsEmpty(headervalue) {
                req.Header.Set(header, headervalue)
        }

        resp, err := client.Do(req)
        if err != nil {
                mu.Lock()
                totalErrors++
                mu.Unlock()
                return
        }
        defer resp.Body.Close()


        headerString := string(resp.Header.Get(ExtractedHeaderValue))

        match, _ := regexp.MatchString(headerPattern, headerString)

        if (!reverse && match) || (reverse && !match) {
         if !quiet {
           fmt.Printf("\n\n%s%s%s\n", Blue, u.String(), Reset)
           for k, v := range resp.Header {
           fmt.Printf("%s%s:%s %v\n", Green, k, Reset, v)
        }
    } else {
        fmt.Printf("%s%s%s\n", Blue, u.String(), Reset)
    }
    checkReflectedParameters(u, resp)
}

        if headerFrequency {
                updateHeaderCount(resp.Header)
        }

        io.Copy(io.Discard, resp.Body)

        mu.Lock()
        totalRequests++
        mu.Unlock()
}

func checkReflectedParameters(u *url.URL, resp *http.Response) {
        queryParams := u.Query()
        headers := resp.Header
        for param, values := range queryParams {
                for _, value := range values {
                        if len(value) < 4 {
                                continue
                        }
                        for headerName, headerValues := range headers {
                                for _, headerValue := range headerValues {
                                        if strings.Contains(headerValue, value) {
                                                if (resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound) && headerName == "Location" {
                                                        continue
                                                }
                                                fmt.Printf("\n%sAlert! Parameter %s with value %s is reflected in header: %s%s\n", Red, param, value, headerValue, Reset)
                                        }
                                }
                        }
                }
        }
}

func containsEmpty(ss ...string) bool {
        for _, s := range ss {
                if s == "" {
                        return true
                }
        }
        return false
}

func updateHeaderCount(headers http.Header) {
        mu.Lock()
        defer mu.Unlock()

        for headerName := range headers {
                headerCount[headerName]++
        }
}

func displayHeaderFrequency() {
        mu.Lock()
        defer mu.Unlock()

        type headerFrequency struct {
                HeaderName  string
                Frequency int
        }

        headerFrequencies := make([]headerFrequency, 0, len(headerCount))

        for headerName, frequency := range headerCount {
                headerFrequencies = append(headerFrequencies, headerFrequency{HeaderName: headerName, Frequency: frequency})
        }

        sort.Slice(headerFrequencies, func(i, j int) bool {
                return headerFrequencies[i].Frequency < headerFrequencies[j].Frequency
        })

        fmt.Println("\n\n----------------------------\nHeader frequency statistics:\n----------------------------")

        for i := 0; i < topRareHeaders && i < len(headerFrequencies); i++ {
                fmt.Printf("%s%s:%s %s%d%s\n", Green, headerFrequencies[i].HeaderName, Reset, Blue, headerFrequencies[i].Frequency, Reset)
        }
}

func printCounters() {
    mu.Lock()
    if quiet {
        fmt.Printf("\r(Total requests: %d  %sErrors: %d%s)", totalRequests, Red, totalErrors, Reset)
    } else {
        fmt.Printf("\r(Total requests: %d  %sErrors: %d%s)\n\x1b[A", totalRequests, Red, totalErrors, Reset)
    }
    mu.Unlock()
}
