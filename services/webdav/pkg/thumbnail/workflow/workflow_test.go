package workflow

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	gatewayv1beta1 "github.com/cs3org/go-cs3apis/cs3/gateway/v1beta1"
	userv1beta1 "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	providerv1beta1 "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	types "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"
	"github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	revactx "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/status"
	"github.com/opencloud-eu/reva/v2/pkg/rgrpc/todo/pool"
	cs3mocks "github.com/opencloud-eu/reva/v2/tests/cs3mocks/mocks"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"

	"github.com/opencloud-eu/opencloud/pkg/log"
	"github.com/opencloud-eu/opencloud/services/webdav/pkg/constants"
	"github.com/opencloud-eu/opencloud/services/webdav/pkg/dav/requests"
	"github.com/opencloud-eu/opencloud/services/webdav/pkg/generator"
	"github.com/opencloud-eu/opencloud/services/webdav/pkg/thumbnail/cache"
)

// testJPEG returns a valid JPEG of the given dimensions.
func testJPEG(w, h int) []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

var _ = Describe("processorID", func() {
	It("maps the default (empty) processor to the thumbnail id", func() {
		Expect(processorID("")).To(Equal("thumbnail"))
	})

	It("maps an explicit thumbnail processor to the same id as the default", func() {
		Expect(processorID("thumbnail")).To(Equal("thumbnail"))
	})

	It("gives opt-in processors their own id so they cache separately", func() {
		Expect(processorID("fit")).To(Equal("fit"))
		Expect(processorID("fill")).To(Equal("fill"))
	})
})

var _ = Describe("matchOperation", func() {
	w := &ThumbnailWorkflow{}

	It("stretches for the resize processor", func() {
		op, aIgnored := w.matchOperation(&requests.ThumbnailRequest{Processor: "resize"}, "image/jpeg")
		Expect(op).To(Equal(generator.OpStretch))
		Expect(aIgnored).To(BeFalse())
	})

	It("fills for the fill processor", func() {
		op, _ := w.matchOperation(&requests.ThumbnailRequest{Processor: "fill"}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFill))
	})

	It("treats the thumbnail processor as fill (square tiles)", func() {
		op, _ := w.matchOperation(&requests.ThumbnailRequest{Processor: "thumbnail"}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFill))
	})

	It("fits in for the fit processor", func() {
		op, _ := w.matchOperation(&requests.ThumbnailRequest{Processor: "fit"}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFitIn))
	})

	It("defaults to fill for non-gif sources without a processor", func() {
		op, aIgnored := w.matchOperation(&requests.ThumbnailRequest{}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFill))
		Expect(aIgnored).To(BeFalse())
	})

	It("stretches gifs by default (resize for gifs)", func() {
		op, _ := w.matchOperation(&requests.ThumbnailRequest{}, "image/gif")
		Expect(op).To(Equal(generator.OpStretch))
	})

	It("reports aIgnored when an explicit processor overrides the legacy a flag", func() {
		// a=0 (Aspect=false) wants fill; fit processor overrides it with fit-in.
		op, aIgnored := w.matchOperation(&requests.ThumbnailRequest{Processor: "fit"}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFitIn))
		Expect(aIgnored).To(BeTrue())

		// a=1 (Aspect=true) wants fit-in; fill processor overrides it with fill.
		op, aIgnored = w.matchOperation(&requests.ThumbnailRequest{Processor: "fill", Aspect: true}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFill))
		Expect(aIgnored).To(BeTrue())

		// a=0 wants fill; thumbnail processor implies fill -> no contradiction.
		op, aIgnored = w.matchOperation(&requests.ThumbnailRequest{Processor: "thumbnail", Aspect: false}, "image/jpeg")
		Expect(op).To(Equal(generator.OpFill))
		Expect(aIgnored).To(BeFalse())

		// resize is neutral: never reports a contradiction.
		_, aIgnored = w.matchOperation(&requests.ThumbnailRequest{Processor: "resize", Aspect: false}, "image/jpeg")
		Expect(aIgnored).To(BeFalse())
	})
})

func newTestRequest(path string, token string, filePath string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set(revactx.TokenHeader, token)

	ctx := req.Context()
	ctx = context.WithValue(ctx, constants.ContextKeyPath, filePath)
	ctx = context.WithValue(ctx, constants.ContextKeyID, "storageid$spaceid!opaqueid")
	req = req.WithContext(ctx)

	return req
}

func newPublicLinkRequest(url string, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, url, nil)

	ctx := req.Context()
	filePath := strings.TrimPrefix(url, "/dav/public-files/"+token+"/")
	if idx := strings.Index(filePath, "?"); idx >= 0 {
		filePath = filePath[:idx]
	}
	ctx = context.WithValue(ctx, constants.ContextKeyPath, filePath)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "token")
	rctx.URLParams.Values = append(rctx.URLParams.Values, token)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req = req.WithContext(ctx)

	return req
}

// setupPreprocessTest wires the gateway mock for a file with the given mime
// type and returns a workflow, generator server and a capture of what the
// generator received.
func setupPreprocessTest(
	t GinkgoTInterface,
	gatewayClient *cs3mocks.GatewayAPIClient,
	logger log.Logger,
	testCache cache.ThumbnailCache,
	mimeType string,
	filePath string,
	downloadBody []byte,
) (*ThumbnailWorkflow, *capturedUpload) {
	wf, err := NewWorkflow(
		WithGeneratorURL("http://generator-test"),
		WithCache(testCache),
		WithHTTPClient(&http.Client{}),
		WithWebdavNamespace("/users/{{.Id.OpaqueId}}"),
		WithLogger(logger),
		WithStater(NewGatewayStater(pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
			"GatewaySelector", "test.gateway",
			func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient })),
		),
		WithFileDownloader(NewGatewayFileDownloader(pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
			"GatewaySelector", "test.gateway",
			func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }), &http.Client{})),
	)
	Expect(err).ToNot(HaveOccurred())

	gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
		return strings.Contains(req.Ref.Path, filePath)
	})).Return(&providerv1beta1.StatResponse{
		Status: status.NewOK(context.Background()),
		Info: &providerv1beta1.ResourceInfo{
			Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
			MimeType: mimeType,
			Size:     uint64(len(downloadBody)),
			Checksum: &providerv1beta1.ResourceChecksum{Sum: "ppchecksum"},
		},
	}, nil)

	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(downloadBody)
	}))
	t.Cleanup(storageServer.Close)

	gatewayClient.On("InitiateFileDownload", mock.Anything, mock.Anything).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
		Status: status.NewOK(context.Background()),
		Protocols: []*gatewayv1beta1.FileDownloadProtocol{
			{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
		},
	}, nil)

	captured := &capturedUpload{}
	generatorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			for _, fh := range r.MultipartForm.File["image"] {
				captured.filename = filepath.Base(fh.Filename)
				file, ferr := fh.Open()
				if ferr != nil {
					continue
				}
				captured.data, _ = io.ReadAll(file)
				file.Close()
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("thumbnail-output"))
	}))
	t.Cleanup(generatorSrv.Close)

	wf.generatorURL = generatorSrv.URL
	return wf, captured
}

type capturedUpload struct {
	path     string
	filename string
	data     []byte
}

var _ = Describe("ThumbnailWorkflow", func() {
	var (
		wf            *ThumbnailWorkflow
		gatewayClient *cs3mocks.GatewayAPIClient
		generatorSrv  *httptest.Server
		logger        log.Logger
		testToken     string
		testCache     cache.ThumbnailCache
	)

	const publicLinkToken = "abc123public"

	BeforeEach(func() {
		pool.RemoveSelector("GatewaySelector" + "test.gateway")
		gatewayClient = &cs3mocks.GatewayAPIClient{}
		gatewaySelector := pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
			"GatewaySelector",
			"test.gateway",
			func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient {
				return gatewayClient
			},
		)

		logger = log.NewLogger()
		testToken = "test-reva-token"
		testCache = cache.NewInMemoryCache()

		var err error
		wf, err = NewWorkflow(
			WithGeneratorURL("http://generator-test"),
			WithCache(testCache),
			WithHTTPClient(&http.Client{}),
			WithWebdavNamespace("/users/{{.Id.OpaqueId}}"),
			WithLogger(logger),
			WithStater(NewGatewayStater(gatewaySelector)),
			WithFileDownloader(NewGatewayFileDownloader(gatewaySelector, &http.Client{})),
		)
		Expect(err).ToNot(HaveOccurred())

		gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
			return strings.Contains(req.Ref.Path, "test.jpg") || strings.Contains(req.Ref.Path, "test.png")
		})).Return(&providerv1beta1.StatResponse{
			Status: status.NewOK(context.Background()),
			Info: &providerv1beta1.ResourceInfo{
				Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
				MimeType: "image/jpeg",
				Size:     1024,
				Checksum: &providerv1beta1.ResourceChecksum{
					// SHA-1 of "test.jpg", used as a realistic content checksum so the on-disk cache shard looks like a normal hash.
					Sum:  "cb56752477cae6405f85b131872c60d21b967c6a",
					Type: providerv1beta1.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_SHA1,
				},
			},
		}, nil)
	})

	AfterEach(func() {
		if generatorSrv != nil {
			generatorSrv.Close()
		}
		pool.RemoveSelector("GatewaySelector" + "test.gateway")
	})

	Describe("full miss path", func() {
		It("should download file, call generator, cache result, and return thumbnail", func() {
			downloadBody := testJPEG(800, 600)

			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Header.Get(revactx.TokenHeader)).To(Equal(testToken))
				w.WriteHeader(http.StatusOK)
				w.Write(downloadBody)
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.Anything).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{
						Protocol:         "spaces",
						DownloadEndpoint: storageServer.URL,
						Token:            "download-token",
					},
				},
			}, nil)

			thumbBytes := []byte("thumbnail-output")

			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.Header.Get("Content-Type")).To(ContainSubstring("multipart/form-data"))

				body, _ := io.ReadAll(r.Body)
				Expect(body).To(ContainElement(downloadBody[0]))

				w.WriteHeader(http.StatusOK)
				w.Write(thumbBytes)
			}))
			defer generatorSrv.Close()

			wf.generatorURL = generatorSrv.URL

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/test.jpg",
				Filename:   "test.jpg",
				Extension:  ".jpg",
				Width:      128,
				Height:     128,
				Aspect:     true,
				Identifier: "storageid$spaceid!opaqueid",
			}

			data, ext, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(Equal(thumbBytes))
			Expect(ext).To(Equal("jpeg"))

			cached, err := testCache.Get("cb/56/752477cae6405f85b131872c60d21b967c6a/128x128-fill.jpeg")
			Expect(err).ToNot(HaveOccurred())
			Expect(cached).To(Equal(thumbBytes))
		})
	})

	Describe("space-scoped reference", func() {
		It("anchors stat and download at the space ResourceId, not a path mount", func() {
			const (
				spaceID = "f7a965c2-17ad-476c-893c-f8aa2fbfec3a"
				storage = "storage-users-1"
			)
			ssid := storage + "$" + spaceID

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				rid := req.GetRef().GetResourceId()
				return rid != nil &&
					rid.StorageId == storage &&
					rid.SpaceId == spaceID &&
					req.GetRef().Path == "./photo.jpeg"
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{Sum: "spacechecksum"},
				},
			}, nil)

			downloadBody := testJPEG(800, 600)
			var downloadRef *providerv1beta1.Reference

			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(downloadBody)
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.InitiateFileDownloadRequest) bool {
				downloadRef = req.GetRef()
				rid := downloadRef.GetResourceId()
				return rid != nil && rid.StorageId == storage && rid.SpaceId == spaceID
			})).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
				},
			}, nil)

			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("space-thumbnail"))
			}))
			defer generatorSrv.Close()
			wf.generatorURL = generatorSrv.URL

			tr := &requests.ThumbnailRequest{
				Filepath:   "photo.jpeg",
				Filename:   "photo.jpeg",
				Extension:  ".jpeg",
				Width:      36,
				Height:     36,
				Identifier: ssid,
			}

			data, ext, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(ext).To(Equal("jpeg"))
			Expect(data).To(Equal([]byte("space-thumbnail")))

			Expect(downloadRef.GetResourceId().StorageId).To(Equal(storage))
			Expect(downloadRef.GetResourceId().SpaceId).To(Equal(spaceID))
		})
	})

	Describe("username-scoped reference", func() {
		It("resolves a username identifier to the user's home path via GetUserByClaim", func() {
			var statRef *providerv1beta1.Reference

			gatewayClient.On("GetUserByClaim", mock.Anything, mock.MatchedBy(func(req *userv1beta1.GetUserByClaimRequest) bool {
				return req.GetClaim() == "username" && req.GetValue() == "alice"
			})).Return(&userv1beta1.GetUserByClaimResponse{
				Status: status.NewOK(context.Background()),
				User: &userv1beta1.User{
					Id: &userv1beta1.UserId{
						Idp:      "https://opencloud-server:9200",
						OpaqueId: "alice-opaque",
						Type:     userv1beta1.UserType_USER_TYPE_PRIMARY,
					},
					Username: "alice",
				},
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				statRef = req.GetRef()
				return req.GetRef().GetResourceId() == nil && strings.HasPrefix(req.GetRef().Path, "/users/alice-opaque/")
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{Sum: "userchecksum"},
				},
			}, nil)

			downloadBody := testJPEG(800, 600)
			var downloadRef *providerv1beta1.Reference

			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(downloadBody)
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.InitiateFileDownloadRequest) bool {
				downloadRef = req.GetRef()
				return req.GetRef().GetResourceId() == nil && strings.HasPrefix(req.GetRef().Path, "/users/alice-opaque/")
			})).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
				},
			}, nil)

			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("user-thumbnail"))
			}))
			defer generatorSrv.Close()
			wf.generatorURL = generatorSrv.URL

			tr := &requests.ThumbnailRequest{
				Filepath:   "photo.jpeg",
				Filename:   "photo.jpeg",
				Extension:  ".jpeg",
				Width:      36,
				Height:     36,
				Identifier: "alice",
			}

			data, ext, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(ext).To(Equal("jpeg"))
			Expect(data).To(Equal([]byte("user-thumbnail")))

			Expect(statRef.GetResourceId()).To(BeNil())
			Expect(statRef.Path).To(Equal("/users/alice-opaque/photo.jpeg"))
			Expect(downloadRef.GetResourceId()).To(BeNil())
			Expect(downloadRef.Path).To(Equal("/users/alice-opaque/photo.jpeg"))
		})
	})

	Describe("preprocessing", func() {
		It("passes direct image mimes through to the generator unchanged", func() {
			pngBytes, err := os.ReadFile(filepath.Join("..", "..", "preprocessor", "test_assets", "noise.png"))
			Expect(err).ToNot(HaveOccurred())

			wf2, captured := setupPreprocessTest(GinkgoT(), gatewayClient, logger, testCache, "image/png", "img.png", pngBytes)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/img.png",
				Filename:   "img.png",
				Extension:  ".png",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err = wf2.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(captured.data).To(Equal(pngBytes))
		})

		It("converts audio to cover art before posting", func() {
			mp3Bytes, err := os.ReadFile(filepath.Join("..", "..", "preprocessor", "test_assets", "empty.mp3"))
			Expect(err).ToNot(HaveOccurred())

			wf2, captured := setupPreprocessTest(GinkgoT(), gatewayClient, logger, testCache, "audio/mpeg", "song.mp3", mp3Bytes)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/song.mp3",
				Filename:   "song.mp3",
				Extension:  ".mp3",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err = wf2.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(captured.data).ToNot(BeEmpty())
			Expect(captured.data).ToNot(Equal(mp3Bytes))
		})

		It("extracts the embedded thumbnail from geogebra slides", func() {
			ggsBytes, err := os.ReadFile(filepath.Join("..", "..", "preprocessor", "test_assets", "ggs_test.ggs"))
			Expect(err).ToNot(HaveOccurred())

			wf2, captured := setupPreprocessTest(GinkgoT(), gatewayClient, logger, testCache, "application/vnd.geogebra.slides", "deck.ggs", ggsBytes)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/deck.ggs",
				Filename:   "deck.ggs",
				Extension:  ".ggs",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err = wf2.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(captured.data).ToNot(BeEmpty())
			Expect(captured.data).ToNot(Equal(ggsBytes))
		})

		It("renders text files to an image", func() {
			text := []byte("This is a test text for thumbnail rendering")

			wf2, captured := setupPreprocessTest(GinkgoT(), gatewayClient, logger, testCache, "text/plain", "notes.txt", text)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/notes.txt",
				Filename:   "notes.txt",
				Extension:  ".txt",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err := wf2.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(captured.data).ToNot(BeEmpty())
			Expect(captured.data).ToNot(Equal(text))
		})

		It("re-encodes gif files to gif bytes before posting", func() {
			gifBytes, err := os.ReadFile(filepath.Join("..", "..", "preprocessor", "test_assets", "noise.gif"))
			Expect(err).ToNot(HaveOccurred())

			wf2, captured := setupPreprocessTest(GinkgoT(), gatewayClient, logger, testCache, "image/gif", "anim.gif", gifBytes)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/anim.gif",
				Filename:   "anim.gif",
				Extension:  ".gif",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err = wf2.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(captured.data).ToNot(BeEmpty())
			// The generator must receive valid gif bytes (GIF87a/GIF89a magic).
			Expect(string(captured.data[:6])).To(SatisfyAny(
				Equal("GIF89a"),
				Equal("GIF87a"),
			))
		})

		It("returns an error when the file cannot be converted to an image", func() {
			wf2, _ := setupPreprocessTest(GinkgoT(), gatewayClient, logger, testCache, "audio/mpeg", "bad.mp3", []byte("not a real audio file"))

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/bad.mp3",
				Filename:   "bad.mp3",
				Extension:  ".mp3",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err := wf2.Execute(context.Background(), tr, testToken, logger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not get image"))
		})
	})

	Describe("cache hit shortcut", func() {
		It("should return cached thumbnail without calling generator or downloading the file", func() {
			cachedThumb := []byte("cached-thumbnail")
			err := testCache.Put("cb/56/752477cae6405f85b131872c60d21b967c6a/128x128-fill.jpeg", cachedThumb)
			Expect(err).ToNot(HaveOccurred())

			// No download expectations are set up. If the workflow tried to
			// download the source on a cache hit, the gateway mock would fail.
			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/test.jpg",
				Filename:   "test.jpg",
				Extension:  ".jpg",
				Width:      128,
				Height:     128,
				Aspect:     true,
				Identifier: "storageid$spaceid!opaqueid",
			}

			data, ext, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(Equal(cachedThumb))
			Expect(ext).To(Equal("jpeg"))
		})
	})

	Describe("mime type rejection", func() {
		It("should return error for unsupported mime types", func() {
			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "document.pdf")
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "application/pdf",
					Size:     2048,
					Checksum: &providerv1beta1.ResourceChecksum{Sum: "pdfchecksum"},
				},
			}, nil)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/document.pdf",
				Filename:   "document.pdf",
				Extension:  ".pdf",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported mime type"))
		})
	})

	Describe("file size rejection", func() {
		It("should return error for files exceeding max input size", func() {
			wfWithLimit, err := NewWorkflow(
				WithGeneratorURL("http://generator-test"),
				WithCache(testCache),
				WithHTTPClient(&http.Client{}),
				WithMaxInputSize(1024),
				WithWebdavNamespace("/users/{{.Id.OpaqueId}}"),
				WithLogger(logger),
				WithStater(NewGatewayStater(pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
					"GatewaySelector", "test.gateway",
					func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient })),
				),
				WithFileDownloader(NewGatewayFileDownloader(pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
					"GatewaySelector", "test.gateway",
					func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }), &http.Client{})),
			)
			Expect(err).ToNot(HaveOccurred())

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "large.jpg")
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     2048,
					Checksum: &providerv1beta1.ResourceChecksum{Sum: "largechecksum"},
				},
			}, nil)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/large.jpg",
				Filename:   "large.jpg",
				Extension:  ".jpg",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err = wfWithLimit.Execute(context.Background(), tr, testToken, logger)
			Expect(errors.Is(err, ErrImageTooLarge)).To(BeTrue())
		})
	})

	Describe("resolution passthrough", func() {
		It("should forward the requested dimensions to the generator unchanged", func() {
			thumbBytes := []byte("matched-thumbnail")

			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(testJPEG(800, 600))
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.Anything).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
				},
			}, nil)

			var receivedURL string
			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedURL = r.URL.Path
				w.WriteHeader(http.StatusOK)
				w.Write(thumbBytes)
			}))
			defer generatorSrv.Close()

			wf.generatorURL = generatorSrv.URL

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/test.jpg",
				Filename:   "test.jpg",
				Extension:  ".jpg",
				Width:      100,
				Height:     100,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(receivedURL).To(ContainSubstring("100x100"))
		})

		It("passes the requested resolution to the generator even when the source is smaller", func() {
			// webdav no longer inspects the image to resolve a "matched" size; it
			// forwards the requested resolution and the generator owns the
			// no-upscale behavior for small sources.
			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(testJPEG(32, 32))
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.Anything).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
				},
			}, nil)

			var receivedURL string
			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedURL = r.URL.Path
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("matched-thumbnail"))
			}))
			defer generatorSrv.Close()

			wf.generatorURL = generatorSrv.URL

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/test.jpg",
				Filename:   "test.jpg",
				Extension:  ".jpg",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(receivedURL).To(ContainSubstring("64x64"))
		})
	})

	Describe("public link thumbnail support", func() {
		It("should authenticate via publicshares and return thumbnail", func() {
			publicLinkAuth := "public-link-token-123"

			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewOK(context.Background()),
				Token:  publicLinkAuth,
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "/public/"+publicLinkToken)
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{
						// Fixed realistic SHA-1-style content checksum so the on-disk cache shard looks like a normal hash.
						Sum:  "5ba1b086d95a7e0fd8ff4323caee0c412ab07fc3",
						Type: providerv1beta1.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_SHA1,
					},
				},
			}, nil)

			downloadBody := testJPEG(800, 600)

			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(downloadBody)
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.InitiateFileDownloadRequest) bool {
				return strings.Contains(req.Ref.Path, "/public/"+publicLinkToken)
			})).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
				},
			}, nil)

			thumbBytes := []byte("public-thumbnail-output")

			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(thumbBytes)
			}))
			defer generatorSrv.Close()

			wf.generatorURL = generatorSrv.URL

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/folder/photo.jpg?x=64&y=64", publicLinkToken)
			auth, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).ToNot(HaveOccurred())

			tr := &requests.ThumbnailRequest{
				Filepath:        "folder/photo.jpg",
				Filename:        "photo.jpg",
				Extension:       ".jpg",
				Width:           64,
				Height:          64,
				Aspect:          true,
				PublicLinkToken: publicLinkToken,
			}

			data, ext, _, err := wf.ExecutePublic(req.Context(), tr, auth, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(Equal(thumbBytes))
			Expect(ext).To(Equal("jpeg"))

			cached, err := testCache.Get("5b/a1/b086d95a7e0fd8ff4323caee0c412ab07fc3/64x64-fill.jpeg")
			Expect(err).ToNot(HaveOccurred())
			Expect(cached).To(Equal(thumbBytes))
		})

		It("should handle pre-signed public links with signature and expiration", func() {
			publicLinkAuth := "presigned-link-token"

			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" &&
					req.ClientId == publicLinkToken &&
					req.ClientSecret == "signature|abc123sig|exp456exp"
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewOK(context.Background()),
				Token:  publicLinkAuth,
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "/public/"+publicLinkToken)
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{
						Sum:  "5ba1b086d95a7e0fd8ff4323caee0c412ab07fc3",
						Type: providerv1beta1.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_SHA1,
					},
				},
			}, nil)

			downloadBody := testJPEG(800, 600)

			storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(downloadBody)
			}))
			defer storageServer.Close()

			gatewayClient.On("InitiateFileDownload", mock.Anything, mock.Anything).Return(&gatewayv1beta1.InitiateFileDownloadResponse{
				Status: status.NewOK(context.Background()),
				Protocols: []*gatewayv1beta1.FileDownloadProtocol{
					{Protocol: "spaces", DownloadEndpoint: storageServer.URL, Token: "download-token"},
				},
			}, nil)

			thumbBytes := []byte("presigned-thumbnail")

			generatorSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(thumbBytes)
			}))
			defer generatorSrv.Close()

			wf.generatorURL = generatorSrv.URL

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/photo.jpg?x=64&y=64&signature=abc123sig&expiration=exp456exp", publicLinkToken)
			auth, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).ToNot(HaveOccurred())

			tr := &requests.ThumbnailRequest{
				Filepath:        "photo.jpg",
				Filename:        "photo.jpg",
				Extension:       ".jpg",
				Width:           64,
				Height:          64,
				PublicLinkToken: publicLinkToken,
			}

			data, _, _, err := wf.ExecutePublic(req.Context(), tr, auth, logger)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(Equal(thumbBytes))
		})

		It("should reject password-protected public link when auth fails", func() {
			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewPermissionDenied(context.Background(), nil, "password required"),
			}, nil)

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/protected.jpg?x=64&y=64", publicLinkToken)
			_, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("password required"))
		})

		It("should reject expired token when auth fails", func() {
			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewFailedPrecondition(context.Background(), nil, "token expired"),
			}, nil)

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/expired.jpg?x=64&y=64", publicLinkToken)
			_, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expired"))
		})

		It("should return nil error on Head when file is accessible", func() {
			publicLinkAuth := "public-link-token-head"

			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewOK(context.Background()),
				Token:  publicLinkAuth,
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "/public/"+publicLinkToken)
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{
						Sum:  "publicchecksum",
						Type: providerv1beta1.ResourceChecksumType_RESOURCE_CHECKSUM_TYPE_SHA1,
					},
				},
			}, nil)

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/folder/photo.jpg?x=64&y=64", publicLinkToken)
			auth, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).ToNot(HaveOccurred())

			tr := &requests.ThumbnailRequest{
				Filepath:        "folder/photo.jpg",
				Filename:        "photo.jpg",
				Extension:       ".jpg",
				Width:           64,
				Height:          64,
				PublicLinkToken: publicLinkToken,
			}

			err = wf.Head(req.Context(), tr, auth, logger)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error on Head when file does not exist", func() {
			publicLinkAuth := "public-link-token-head"

			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewOK(context.Background()),
				Token:  publicLinkAuth,
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "nonexistent.jpg")
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewNotFound(context.Background(), "file not found"),
			}, nil)

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/nonexistent.jpg?x=64&y=64", publicLinkToken)
			auth, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).ToNot(HaveOccurred())

			tr := &requests.ThumbnailRequest{
				Filepath:        "nonexistent.jpg",
				Filename:        "nonexistent.jpg",
				Extension:       ".jpg",
				Width:           64,
				Height:          64,
				PublicLinkToken: publicLinkToken,
			}

			err = wf.Head(req.Context(), tr, auth, logger)
			Expect(err).To(HaveOccurred())
		})

		It("should return error on Head when mime type is not supported", func() {
			publicLinkAuth := "public-link-token-head"

			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewOK(context.Background()),
				Token:  publicLinkAuth,
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "document.pdf")
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "application/pdf",
					Size:     2048,
				},
			}, nil)

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/document.pdf?x=64&y=64", publicLinkToken)
			auth, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).ToNot(HaveOccurred())

			tr := &requests.ThumbnailRequest{
				Filepath:        "document.pdf",
				Filename:        "document.pdf",
				Extension:       ".pdf",
				Width:           64,
				Height:          64,
				PublicLinkToken: publicLinkToken,
			}

			err = wf.Head(req.Context(), tr, auth, logger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported mime type"))
		})
	})

	Describe("file processing", func() {
		It("returns ErrFileProcessing when the file is still being processed", func() {
			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "processing.jpg")
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{Sum: "processingchecksum"},
					Opaque: &types.Opaque{
						Map: map[string]*types.OpaqueEntry{
							"status": {Decoder: "plain", Value: []byte("processing")},
						},
					},
				},
			}, nil)

			tr := &requests.ThumbnailRequest{
				Filepath:   "/test-space/processing.jpg",
				Filename:   "processing.jpg",
				Extension:  ".jpg",
				Width:      64,
				Height:     64,
				Identifier: "storageid$spaceid!opaqueid",
			}

			_, _, _, err := wf.Execute(context.Background(), tr, testToken, logger)
			Expect(errors.Is(err, ErrFileProcessing)).To(BeTrue())
		})

		It("returns ErrFileProcessing on Head when the file is still being processed", func() {
			gatewayClient.On("Authenticate", mock.Anything, mock.MatchedBy(func(req *gatewayv1beta1.AuthenticateRequest) bool {
				return req.Type == "publicshares" && req.ClientId == publicLinkToken
			})).Return(&gatewayv1beta1.AuthenticateResponse{
				Status: status.NewOK(context.Background()),
				Token:  "public-link-token-head",
			}, nil)

			gatewayClient.On("Stat", mock.Anything, mock.MatchedBy(func(req *providerv1beta1.StatRequest) bool {
				return strings.Contains(req.Ref.Path, "/public/"+publicLinkToken)
			})).Return(&providerv1beta1.StatResponse{
				Status: status.NewOK(context.Background()),
				Info: &providerv1beta1.ResourceInfo{
					Type:     providerv1beta1.ResourceType_RESOURCE_TYPE_FILE,
					MimeType: "image/jpeg",
					Size:     1024,
					Checksum: &providerv1beta1.ResourceChecksum{Sum: "processingchecksum"},
					Opaque: &types.Opaque{
						Map: map[string]*types.OpaqueEntry{
							"status": {Decoder: "plain", Value: []byte("processing")},
						},
					},
				},
			}, nil)

			req := newPublicLinkRequest("/dav/public-files/"+publicLinkToken+"/folder/photo.jpg?x=64&y=64", publicLinkToken)
			auth, err := ResolvePublicLinkAuth(req.Context(), req, publicLinkToken, pool.GetSelector[gatewayv1beta1.GatewayAPIClient](
				"GatewaySelector", "test.gateway",
				func(cc grpc.ClientConnInterface) gatewayv1beta1.GatewayAPIClient { return gatewayClient }))
			Expect(err).ToNot(HaveOccurred())

			tr := &requests.ThumbnailRequest{
				Filepath:        "folder/photo.jpg",
				Filename:        "photo.jpg",
				Extension:       ".jpg",
				Width:           64,
				Height:          64,
				PublicLinkToken: publicLinkToken,
			}

			err = wf.Head(req.Context(), tr, auth, logger)
			Expect(errors.Is(err, ErrFileProcessing)).To(BeTrue())
		})
	})
})
