package webchat

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	transport "github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz"
	"go.uber.org/zap"
)

func TestBrowserAndHostCredentialsCannotUseExistingServiceAPI(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "one")
	engine := server.New()
	handler, err := transport.NewHandler(f.g.app.(*customerservice.Service))
	if err != nil {
		t.Fatal(err)
	}
	serviceToken := "service-" + strings.Repeat("c", 40)
	security, err := transport.NewSecurityMiddleware(serviceToken)
	if err != nil {
		t.Fatal(err)
	}
	access, err := transport.NewAccessLogMiddleware(zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	limit, err := transport.NewRateLimitMiddleware(20, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.RegisterRoutes(engine.Engine, handler, transport.NewAvailability(), security, access, limit); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{s.AccessToken, testHosts[0].Secret} {
		for _, path := range []string{
			"/v1/conversations/arbitrary/messages",
			"/v1/conversations/arbitrary/messages/trigger/reply",
			"/v1/conversations/arbitrary/messages/trigger/reply/stream",
			"/v1/conversations/arbitrary/messages/trigger/reply/eligibility",
		} {
			result := ut.PerformRequest(engine.Engine, http.MethodPost, path, &ut.Body{Body: strings.NewReader(`{}`), Len: 2}, ut.Header{Key: "Authorization", Value: "Bearer " + token}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
			if result.StatusCode() != 401 {
				t.Fatalf("capability accepted at %s: %d", path, result.StatusCode())
			}
		}
	}
	expect(t, call(f.g, "GET", MessagesPath, serviceToken, nil), 401)
	expect(t, call(f.g, "POST", SessionPath, serviceToken, `{"customer_id":"one"}`), 401)
	if f.models.Load() != 0 {
		t.Fatal("wrong credential invoked a model")
	}
}

func TestClosedAndHumanConversationReplay(t *testing.T) {
	for _, status := range []conversation.Status{conversation.StatusClosed, conversation.StatusWaitingHuman} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture(t, Config{}, nil)
			s := mint(t, f, 0, "one")
			send(t, f, s, "first", "question")
			err := f.repo.WithinTx(context.Background(), func(tx conversation.Transaction) error {
				current, err := tx.FindConversation(s.ConversationID)
				if err != nil {
					return err
				}
				if status == conversation.StatusClosed {
					err = current.Close(time.Now())
				} else {
					err = current.RequestHandoff(time.Now())
				}
				if err != nil {
					return err
				}
				return tx.UpdateConversation(current)
			})
			if err != nil {
				t.Fatal(err)
			}
			result := send(t, f, s, "first", "question")
			want := "superseded"
			if status == conversation.StatusWaitingHuman {
				want = "waiting_human"
			}
			if result.Outcome != want {
				t.Fatalf("outcome=%s", result.Outcome)
			}
			if status == conversation.StatusClosed {
				expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, `{"event_id":"new","content":"hello"}`), 409)
			} else if result := send(t, f, s, "new", "human question"); result.Outcome != "waiting_human" {
				t.Fatal(result)
			}
			if f.models.Load() != 1 {
				t.Fatal("replay started a new bot generation")
			}
			if history(t, f, s).Status != status {
				t.Fatal("wrong history status")
			}
		})
	}
}

func TestTimeoutReleasesAdmissionAndKeepsAcceptedMessage(t *testing.T) {
	f := newFixture(t, Config{RequestTimeout: time.Second}, func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
		<-ctx.Done()
		return customerservice.Reply{}, ctx.Err()
	})
	s := mint(t, f, 0, "one")
	w := call(f.g, "POST", MessagesPath, s.AccessToken, `{"event_id":"first","content":"question"}`)
	expect(t, w, 503)
	if len(history(t, f, s).Messages) != 1 {
		t.Fatal("accepted question lost after timeout")
	}
	if f.g.active != 0 {
		t.Fatal("admission leaked")
	}
}

func TestHistoryClipsHugeLegacyContentWithoutBreakingUTF8(t *testing.T) {
	f := newFixture(t, Config{}, func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
		return customerservice.Reply{Content: strings.Repeat("文", 10000)}, nil
	})
	s := mint(t, f, 0, "one")
	send(t, f, s, "first", "question")
	h := history(t, f, s)
	if len(h.Messages) != 2 || !h.Messages[1].ContentTruncated || len(h.Messages[1].Content) > 16<<10 || strings.Contains(h.Messages[1].Content, "�") {
		t.Fatal("invalid clipped history")
	}
}

func TestPerPrincipalSessionBoundAndInvalidBodies(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	for i := 0; i < 8; i++ {
		_ = mint(t, f, 0, "one")
	}
	expect(t, call(f.g, "POST", SessionPath, testHosts[0].Secret, `{"customer_id":"one"}`), 429)
	s := mint(t, f, 0, "another")
	for _, body := range []string{`{"event_id":"x","event_id":"y","content":"ok"}`, `{"EVENT_ID":"x","content":"ok"}`, "{\"event_id\":\"x\",\"content\":\"\xff\"}"} {
		expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body), 400)
	}
	if _, err := f.repo.FindConversation(context.Background(), s.ConversationID); !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatal("invalid body created conversation")
	}
}
