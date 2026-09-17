package pcms

// Config locates the PayloadCMS instance.
type Config struct {
	BaseURL string `env:"payloadCms.baseUrl" required:"true"`
	APIURL  string `env:"payloadCms.apiUrl"  default:"/api"`
}
