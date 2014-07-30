package main

import (
  "bufio"
  "crypto/md5"
  "encoding/json"
  "fmt"
  "html/template"
  "io"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "os/exec"
  "path"
  "strconv"
  "strings"
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

type VideoMetadata struct {
  OriginalFileName string
  Title string
  Description string
  Duration float64
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
    expectedCount, _ := strconv.ParseInt(
        r.FormValue("resumableTotalChunks"), 10, 32)
    filename := r.FormValue("resumableFilename")
    go checkComplete(folderPath, int(expectedCount), filename)
  } else {
    http.Error(w, "Unsupported", 505)
  }
}

func checkComplete(folderPath string, expectedCount int, filename string) {
  fileInfos, _ := ioutil.ReadDir(folderPath)
  if int(expectedCount) == len(fileInfos) {
    fmt.Printf("Got them all!\n")
    outputPath := fmt.Sprintf("/tmp/%s", filename)
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
    os.RemoveAll(folderPath)

    originalBaseName := path.Base(outputPath)
    md5Hash := md5.New()
    io.WriteString(md5Hash, fmt.Sprintf("%s|%d", originalBaseName, 
        time.Now().Unix()))
    basename := fmt.Sprintf("%x", md5Hash.Sum([]byte{}))
    video1080Path := "/tmp/" + basename + "_1080.mp4"
    video720Path := "/tmp/" + basename + "_720.mp4"
    video360Path := "/tmp/" + basename + "_360.mp4"
    cmd := exec.Command("ffmpeg", 
        "-i", outputPath,
        "-s", "1920x1080",
        "-vcodec", "libx264",
        "-acodec", "libfaac",
        video1080Path,

        "-s", "1280x720",
        "-vcodec", "libx264",
        "-acodec", "libfaac",
        video720Path,
        
        "-s", "640x360",
        "-vcodec", "libx264",
        "-acodec", "libfaac",
        video360Path,
    )
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    err := cmd.Run()
    if err != nil {
      fmt.Printf("Could not transcode file: %v\n", err)
      return
    }
    os.RemoveAll(outputPath)
    fmt.Printf("Transcode complete\n")

    // Create a thumbnail
    thumbPath := "/tmp/" + basename + "_thumb.jpg"
    cmd = exec.Command("ffmpeg",
      "-i", video360Path,
      "-ss", "00:00:01",
      "-vframes", "1",
      thumbPath,
    )
    stderr, _ := cmd.StderrPipe()
    scanner := bufio.NewScanner(stderr)
    err = cmd.Start()
    duration := 0.0
    for scanner.Scan() {
      scanned := scanner.Text()
      if strings.Index(scanned, "Duration:") != -1 {
        parts := strings.Split(scanned, ",")
        parts = strings.Split(parts[0], "Duration: ")
        parts = strings.Split(parts[1], ":")
        hours, _ := strconv.ParseFloat(parts[0], 64)
        hours = hours * 3600
        minutes, _ := strconv.ParseFloat(parts[1], 64)
        minutes = minutes * 60
        seconds, _ := strconv.ParseFloat(parts[2], 64)
        duration = hours + minutes + seconds
      }
    }
    cmd.Wait()
    if err != nil {
      fmt.Printf("Could not transcode file: %v\n", err)
      return
    }
    fmt.Printf("Thumbnail complete: %s\n", thumbPath)

    s3Bucket := getS3Bucket()
    for _, videoPath := range [...]string{ thumbPath, video360Path,
        video720Path, video1080Path } {
      videoStat, _ := os.Stat(videoPath)
      uploadFilename := strings.Replace(videoPath, "/tmp", basename, -1)
      videoReader, _ := os.Open(videoPath)
      var contentType string
      if (path.Ext(videoPath) == ".jpg") {
        contentType = "image/jpg"
      } else { 
        contentType = "video/mp4"
      }
      err := s3Bucket.PutReader(uploadFilename, videoReader, videoStat.Size(),
          contentType, s3.PublicRead)
      if err != nil {
        fmt.Printf("Failed to upload %s: %v\n", videoPath, err)
      } else {
        fmt.Printf("Upload of %s complete\n", uploadFilename)
        os.RemoveAll(videoPath)
      }
    }
    jsonMetadata, _ := json.Marshal(VideoMetadata{
      originalBaseName,
      "Untitled",
      "",
      duration,
    })
    _ = s3Bucket.Put("/" + basename + "/metadata.json", 
      []byte(jsonMetadata), "text/json", s3.PublicRead)
    fmt.Printf("Metadata written\n")
  }
}
