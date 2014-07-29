package main

import (
  "encoding/json"
  "fmt"
  "html/template"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "strconv"
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
      AccessKey: config.AccessKey,
      SecretKey: config.SecretKey,
  }

  // Set up web routes
  router := mux.NewRouter()
  router.HandleFunc("/", index).Methods("GET")
  router.HandleFunc("/test_post", testPost).Methods("POST")
  router.HandleFunc("/upload", handleUpload)

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
  s3Bucket := s3Connection.Bucket(config.BucketName)
  return s3Bucket 
}

var templates, _ = template.New("index").ParseFiles("./tmpl/index.html")
func index(w http.ResponseWriter, r *http.Request) {
  data := make(map[string]string)
  templates.ExecuteTemplate(w, "index.html", data)
  /*
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
  */
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

func handleUpload(w http.ResponseWriter, r *http.Request) {
  id := r.FormValue("resumableIdentifier")
  chunkNum, _ := strconv.ParseInt(r.FormValue("resumableChunkNumber"),
      10, 32)

  folderPath := fmt.Sprintf("/tmp/%s", id)
  filePath := fmt.Sprintf("%s/%08d", folderPath, chunkNum)
  if r.Method == "GET" {
    _, err := os.Stat(filePath)
    if err != nil {
      http.Error(w, "No Content", 204)
      return
    }

    fmt.Fprintf(w, "Found")
  } else if r.Method == "POST" {
    err := os.Mkdir(folderPath, 0744)
    file, _, err := r.FormFile("file")
    if err != nil {
      http.Error(w, "Could not read form data", 500)
      return
    }

    data, err := ioutil.ReadAll(file)
    if err != nil {
      fmt.Printf("Could not get form file: %v", err)
      http.Error(w, "Could not read file", 500)
      return
    }
    
    ioutil.WriteFile(filePath, data, 0744)
    if err != nil {
      fmt.Printf("Uh oh: %v", err);
      http.Error(w, "Could not create file", 500)
      return
    }

    fmt.Fprintf(w, "Saved")

    // Check if we are done
    expectedChunkCount, _ := strconv.ParseInt(
        r.FormValue("resumableTotalChunks"), 10, 32)
    fileInfos, err := ioutil.ReadDir(folderPath)
    if int(expectedChunkCount) == len(fileInfos) {
      fmt.Printf("Got them all!\n")
      outputPath := fmt.Sprintf("/tmp/%s", r.FormValue("resumableFilename"))
      output, _ := os.Create(outputPath)
      for _, fileInfo := range fileInfos {
        chunkData, err := ioutil.ReadFile(
            fmt.Sprintf("%s/%s", folderPath, fileInfo.Name()))
        if err != nil {
          fmt.Printf("Error reading chunk: %v", err)
        }
        output.Write(chunkData)
      }
      fmt.Printf("Complete file: %s\n", outputPath)
    }
  } else {
    http.Error(w, "Unsupported", 505)
  }
}
