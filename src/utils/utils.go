package utils

import (
    "net/http"
    "strings"
)

// GetRequestMetadata extracts metadata from an HTTP request.
func GetRequestMetadata(r *http.Request) map[string]string {
    ip, source := getClientIP(r)
    return map[string]string{
        "ip_address": ip,
        "ip_source":  source,
        "user_agent": GetUserAgent(r),
    }
}

func getClientIP(r *http.Request) (string, string) {
    proxyHeaders := []string{
        "CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP", "X-Original-Forwarded-For",
        "Forwarded", "True-Client-IP", "X-Client-IP",
    }
    for _, header := range proxyHeaders {
        if value := r.Header.Get(header); value != "" {
            if header == "X-Forwarded-For" {
                ips := strings.Split(value, ",")
                return strings.TrimSpace(ips[0]), header
            }
            if header == "Forwarded" {
                for _, part := range strings.Split(value, ";") {
                    if strings.HasPrefix(strings.TrimSpace(part), "for=") {
                        ip := strings.Trim(strings.SplitN(part, "=", 2)[1], "\"[]")
                        return strings.Split(ip, ":")[0], header
                    }
                }
            }
            return value, header
        }
    }
    return r.RemoteAddr, "direct"
}

// GetUserAgent extracts the User-Agent header.
func GetUserAgent(r *http.Request) string {
    if ua := r.Header.Get("User-Agent"); ua != "" {
        return ua
    }
    return "Unknown"
}