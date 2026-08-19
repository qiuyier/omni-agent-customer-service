package hertz

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRouterPathsMatchThriftIDL(t *testing.T) {
	idlPath := filepath.Join("..", "..", "..", "idl", "customer_service.thrift")
	idl, err := os.ReadFile(idlPath)
	if err != nil {
		t.Fatalf("read %s: %v", idlPath, err)
	}

	tests := []struct {
		rpc        string
		annotation string
		routerPath string
	}{
		{rpc: "Health", annotation: "api.get", routerPath: healthPath},
		{rpc: "ReceiveMessage", annotation: "api.post", routerPath: receiveMessagePath},
		{rpc: "GenerateBotReply", annotation: "api.post", routerPath: generateReplyPath},
	}
	for _, test := range tests {
		t.Run(test.rpc, func(t *testing.T) {
			pattern := regexp.MustCompile(fmt.Sprintf(
				`(?s)\b%s\s*\([^)]*\)\s*\(\s*%s\s*=\s*"([^"]+)"`,
				regexp.QuoteMeta(test.rpc),
				regexp.QuoteMeta(test.annotation),
			))
			matches := pattern.FindSubmatch(idl)
			if len(matches) != 2 {
				t.Fatalf("IDL route annotation for %s was not found", test.rpc)
			}
			if idlPath := string(matches[1]); idlPath != test.routerPath {
				t.Fatalf("IDL path = %q, router path = %q", idlPath, test.routerPath)
			}
		})
	}
}
