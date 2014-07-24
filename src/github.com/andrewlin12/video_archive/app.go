package main

import (
  "encoding/json"
  "fmt"
  "html/template"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "time"
  "github.com/gorilla/mux"
  "launchpad.net/goamz/aws"
  "launchpad.net/goamz/s3"
)

type JsonConfig struct {
  AccessKey string
  SecretKey string
  BucketName string
}

var config JsonConfig
var s3Auth aws.Auth

func main() {
  // Read config from disk
  configFile, e := ioutil.ReadFile("./config.json")
  if e != nil {
    fmt.Printf("Must have config.json\n")
    os.Exit(1)
  }
  json.Unmarshal(configFile, &config)
  fmt.Printf("AccessKey: %s\n", config.AccessKey)
  fmt.Printf("SecretKey: %s\n", config.SecretKey)
  fmt.Printf("BucketName: %s\n", config.BucketName)

  s3Auth = aws.Auth{
      AccessKey: "AKIAIWDOSH6F6WM4HR5A",
      SecretKey: "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",
  }

  // Set up web routes
  router := mux.NewRouter()
  router.HandleFunc("/", index).Methods("GET")
  router.HandleFunc("/test_post", testPost).Methods("POST")

  // Static routes
  pubFileServer := http.FileServer(http.Dir("./pub/"))
  for _, path := range [...]string{"js", "css", "img"} {
    prefix := fmt.Sprintf("/%s/", path)
    router.PathPrefix(prefix).Handler(pubFileServer).Methods("GET")
  }

  http.Handle("/", router)

  log.Println("Listening...");
  http.ListenAndServe(":3000", nil);
}

func getS3Bucket() *s3.Bucket {
  // Connect to S3
  s3Connection := s3.New(s3Auth, aws.USEast)
  s3Bucket := s3Connection.Bucket("onceonatime_video_archive")
  return s3Bucket 
}

var indexTemplate, _ = template.New("index").Parse("Hello, {{.name}}\n")
func index(w http.ResponseWriter, r *http.Request) {
  s3Bucket := getS3Bucket()
  res, err := s3Bucket.List("", "", "", 1000)
  if err != nil {
      log.Fatal(err)
  }
  fmt.Fprint(w, "<pre>")
  for _, v := range res.Contents {
      fmt.Fprintf(w, "%s\n", v.Key)
  }
  fmt.Fprint(w, "</pre>")
}

func testPost(w http.ResponseWriter, r *http.Request) {
  filename := fmt.Sprintf("test_%d.txt", time.Now().Unix())
  contents := fmt.Sprintf("Posted data: %s", r.FormValue("data"))
  s3Bucket := getS3Bucket()
  err := s3Bucket.Put(filename, []byte(contents), "text/plain", s3.PublicRead)
  if err != nil {
    log.Fatal(err)
  }
  fmt.Fprint(w, "Success!\n");
 }

