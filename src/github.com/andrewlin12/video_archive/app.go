package main

import (
  "bufio"
  "crypto/md5"
  "encoding/json"
  "flag"
  "fmt"
  "html/template"
  "io"
  "io/ioutil"
  "net/http"
  "os"
  "os/exec"
  "path"
  "strconv"
  "strings"
  "sync"
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
  Status string
  DateTaken int64
  DateUploaded int64
}

var config JsonConfig
var s3Auth aws.Auth
var uploadMutex *sync.Mutex
var ffmpegMutex *sync.Mutex

func main() {
  uploadMutex = &sync.Mutex{}
  ffmpegMutex = &sync.Mutex{}

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
  router.HandleFunc("/upload", handleUpload)
  router.HandleFunc("/video/{id}/stripRotateTag", stripRotateTag)
  router.HandleFunc("/video/{id}/rotate/{degrees}", rotate)
  router.HandleFunc("/video/{id}/delete", deleteVideo)
  router.HandleFunc("/video/{id}", video)
  router.HandleFunc("/videos", videos)

  // Static routes
  pubFileServer := http.FileServer(http.Dir("./pub/"))
  for _, path := range [...]string{"js", "css", "img", "tmpl"} {
    prefix := fmt.Sprintf("/%s/", path)
    router.PathPrefix(prefix).Handler(pubFileServer).Methods("GET")
  }

  http.Handle("/", router)

  var port = flag.Int("port", 3000, "Port to listen for requests");
  flag.Parse()

  fmt.Printf("Listening on %d...\n", *port);
  http.ListenAndServe(fmt.Sprintf(":%d", *port), nil);
}

func getS3Bucket() *s3.Bucket {
  // Connect to S3
  s3Connection := s3.New(s3Auth, aws.USEast)
  s3Bucket := s3Connection.Bucket(config.BucketName)
  return s3Bucket
}

var templates, _ = template.New("index").ParseFiles("./tmpl/index.html")
func index(w http.ResponseWriter, r *http.Request) {
  templates.ExecuteTemplate(w, "index.html", nil)
}

type VideosJson struct {
  Bucket string
  Ids map[string]string
  Remaining int
}
func videos(w http.ResponseWriter, r *http.Request) {
  qs := r.URL.Query()
  skip := 0
  if qs["skip"] != nil {
    skip64, err := strconv.ParseInt(qs["skip"][0], 10, 32)
    if err != nil {
      http.Error(w, "Invalid 'skip' parameter", 400)
      return
    }
    skip = int(skip64)
  }
  limit := 1000
  if qs["limit"] != nil {
    limit64, err := strconv.ParseInt(qs["limit"][0], 10, 32)
    if err != nil {
      http.Error(w, "Invalid 'limit' parameter", 400)
      return
    }
    limit = int(limit64)
  }

  s3Bucket := getS3Bucket()
  res, _ := s3Bucket.List("", "/", "", 1000)
  maxLimit := len(res.CommonPrefixes) - skip
  if limit > maxLimit {
    limit = maxLimit
  }
  startIndex := len(res.CommonPrefixes) - skip - limit
  if startIndex < 0 {
    startIndex = 0
  }
  prefixes := res.CommonPrefixes[startIndex:startIndex + limit]
  keys := make(map[string]string)
  var wg sync.WaitGroup
  i := len(prefixes) - 1
  for i >= 0 {
    j := 0
    for j < 50 {
      if i - j < 0 {
        break
      }
      v := prefixes[i - j]
      key := strings.Replace(v, "/", "", -1)
      wg.Add(1)
      go func(key string) {
        resp, err := http.Get(
            fmt.Sprintf("http://s3.amazonaws.com/%s/%s/metadata.json",
            config.BucketName, key))
        if err != nil {
          fmt.Printf("%v\n", err)
        } else {
          defer resp.Body.Close()
          body, _ := ioutil.ReadAll(resp.Body)
          keys[key] = string(body)
        }
        wg.Done()
      }(key)
      j++
    }
    wg.Wait()
    i -= j 
  }

  w.Header().Set("Content-Type", "application/json")
  json.NewEncoder(w).Encode(VideosJson{
    Bucket: config.BucketName,
    Ids: keys,
    Remaining: startIndex,
  })
}

func video(w http.ResponseWriter, r *http.Request) {
  vars := mux.Vars(r)
  s3Bucket := getS3Bucket()
  data, err := s3Bucket.Get(vars["id"] + "/metadata.json")
  if err != nil {
    http.Error(w, "Not Found", 404)
    return
  }
  w.Header().Set("Content-Type", "application/json")
  w.Write(data)
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

    // Check if we are done
    expectedCount, _ := strconv.ParseInt(
        r.FormValue("resumableTotalChunks"), 10, 32)
    filename := r.FormValue("resumableFilename")

    // NOTE: Need to write the file and check if we are done in a mutex
    uploadMutex.Lock()
    err = ioutil.WriteFile(filePath, data, 0744)
    fileInfos, _ := ioutil.ReadDir(folderPath)
    uploadMutex.Unlock()

    if err != nil {
      fmt.Printf("Uh oh: %v", err);
      http.Error(w, "Could not create file", 500)
      return
    }


    if int(expectedCount) == len(fileInfos) {
      uploadComplete(w, folderPath, filename, fileInfos)
    }
    fmt.Fprintf(w, "Saved")
  } else {
    http.Error(w, "Unsupported", 505)
  }
}

func uploadComplete(w http.ResponseWriter, folderPath string, 
    filename string, fileInfos []os.FileInfo) {
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

  // ffprobe some metadata out
  cmd := exec.Command("ffprobe",
    "-show_streams",
    "-show_format",
    "-i", outputPath,
  )
  stdout, _ := cmd.StdoutPipe()
  scanner := bufio.NewScanner(stdout)
  err := cmd.Start()
  duration := 0.0
  dateTaken := time.Now().Unix() 
  width := 1920
  height := 1080
  for scanner.Scan() {
    scanned := scanner.Text()
    if strings.Index(scanned, "duration=") != -1 {
      parts := strings.SplitN(scanned, "=", 2)
      duration, err = strconv.ParseFloat(parts[1], 64)
    } else if strings.Index(scanned, "TAG:creation_time") != -1 {
      parts := strings.SplitN(scanned, "creation_time=", 2)
      parsedDate, err := time.Parse("2006-01-02 15:04:05", 
          strings.TrimSpace(parts[1]))
      if err == nil {
        dateTaken = parsedDate.Unix()
      }
    } else if strings.Index(scanned, "TAG:date=") != -1 {
      parts := strings.SplitN(scanned, "date=", 2)
      parsedDate, err := time.Parse("2006-01-02T15:04:05-0700", 
          strings.TrimSpace(parts[1]))
      if err == nil {
        dateTaken = parsedDate.Unix()
      }
    } else if strings.Index(scanned, "width=") != -1 {
      parts := strings.SplitN(scanned, "=", 2)
      newWidth, err := strconv.ParseInt(parts[1], 10, 32)
      if err == nil {
        width = int(newWidth)
      }
    } else if strings.Index(scanned, "height=") != -1 {
      parts := strings.SplitN(scanned, "=", 2)
      newHeight, err := strconv.ParseInt(parts[1], 10, 32)
      if err == nil {
        height = int(newHeight)
      }
    }
  }
  cmd.Wait()

  var dims1920, dims1280, dims640 string
  if width > height {
    dims1920 = fmt.Sprintf("1920x%d", 1920 * height / width / 2 * 2)
    dims1280 = fmt.Sprintf("1280x%d", 1280 * height / width / 2 * 2)
    dims640 = fmt.Sprintf("640x%d", 640 * height / width / 2 * 2)
  } else {
    dims1920 = fmt.Sprintf("%dx1920", 1920 * width / height / 2 * 2)
    dims1280 = fmt.Sprintf("%dx1280", 1280 * width / height / 2 * 2)
    dims640 = fmt.Sprintf("%dx640", 640 * width / height / 2 * 2)
  }

  originalBaseName := path.Base(outputPath)
  md5Hash := md5.New()
  io.WriteString(md5Hash, fmt.Sprintf("%s|%d", originalBaseName, 
      time.Now().Unix()))
  basename := fmt.Sprintf("%d_%x", dateTaken, md5Hash.Sum([]byte{}))

  // Create a thumbnail
  thumbPath := "/tmp/" + basename + "_thumb.jpg"
  cmd = exec.Command("ffmpeg",
    "-i", outputPath,
    "-vframes", "1",
    "-s", dims640,
    thumbPath,
  )
  err = cmd.Run();
  if err != nil {
    fmt.Printf("Could not generate thumbnail: %v\n", err)
    return
  }
  fmt.Printf("Thumbnail complete: %s\n", thumbPath)
  uploadVideoFile(thumbPath, basename)

  metadata := VideoMetadata{
    Title: originalBaseName,
    OriginalFileName: originalBaseName,
    Description: fmt.Sprintf("Recorded %s", 
        time.Unix(dateTaken, 0).Format("Jan 2, 2006 3:04PM")),
    Duration: duration,
    DateTaken: dateTaken,
    DateUploaded: time.Now().Unix(),
    Status: "Processing",
  }
  jsonMetadata, _ := json.Marshal(metadata)

  _ = getS3Bucket().Put("/" + basename + "/metadata.json", 
    []byte(jsonMetadata), "text/json", s3.PublicRead)
  fmt.Printf("Metadata written\n")

  // NOTE: Do video transcodes in a goroutine
  go func() {
    video1080Path := "/tmp/" + basename + "_1080.mp4"
    video720Path := "/tmp/" + basename + "_720.mp4"
    video360Path := "/tmp/" + basename + "_360.mp4"
    cmd := exec.Command("ffmpeg", 
        "-i", outputPath,
        "-y",
        "-s", dims1920,
        "-vcodec", "libx264",
        "-acodec", "libfaac",
        video1080Path,

        "-y",
        "-s", dims1280,
        "-vcodec", "libx264",
        "-acodec", "libfaac",
        video720Path,

        "-y",
        "-s", dims640,
        "-vcodec", "libx264",
        "-acodec", "libfaac",
        video360Path,
    )
    // HACK: Only allow a single ffmpeg transcode to run at a time.  Should
    //       really use some kind of queueing system (which would also)
    //       allow recovery, but this works for now
    ffmpegMutex.Lock()
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    err := cmd.Run()
    ffmpegMutex.Unlock()
    if err != nil {
      fmt.Printf("Could not transcode file: %v\n", err)
      return
    }
    os.RemoveAll(outputPath)
    fmt.Printf("Transcode complete\n")

    for _, videoPath := range [...]string{ video360Path,
        video720Path, video1080Path } {
      uploadVideoFile(videoPath, basename)
    }

    metadata.Status = "Ready"
    jsonMetadata, _ := json.Marshal(metadata)
    _ = getS3Bucket().Put("/" + basename + "/metadata.json", 
        []byte(jsonMetadata), "text/json", s3.PublicRead)
    fmt.Printf("Final metadata written\n")
  }()
}

func uploadVideoFile(filePath string, basename string) {
  stat, _ := os.Stat(filePath)
  uploadFilename := strings.Replace(filePath, "/tmp", basename, -1)
  reader, _ := os.Open(filePath)
  var contentType string
  if (path.Ext(filePath) == ".jpg") {
    contentType = "image/jpg"
  } else { 
    contentType = "video/mp4"
  }
  err := getS3Bucket().PutReader(uploadFilename, reader, stat.Size(),
      contentType, s3.PublicRead)
  if err != nil {
    fmt.Printf("Failed to upload %s: %v\n", filePath, err)
  } else {
    fmt.Printf("Upload of %s complete\n", uploadFilename)
    os.RemoveAll(filePath)
  }
}

func deleteVideo(w http.ResponseWriter, r *http.Request) {
  vars := mux.Vars(r)
  basename := vars["id"]
  s3Bucket := getS3Bucket()

  s3Bucket.Del(fmt.Sprintf("%s/%s_1080.mp4", basename, basename))
  s3Bucket.Del(fmt.Sprintf("%s/%s_720.mp4", basename, basename))
  s3Bucket.Del(fmt.Sprintf("%s/%s_360.mp4", basename, basename))
  s3Bucket.Del(fmt.Sprintf("%s/%s_thumb.jpg", basename, basename))
  s3Bucket.Del(fmt.Sprintf("%s/metadata.json", basename))
  fmt.Fprintf(w, "Deleted")
}

func rotate(w http.ResponseWriter, r *http.Request) {
  vars := mux.Vars(r)
  basename := vars["id"]
  degrees := vars["degrees"]
  videoFilters := "transpose=4"
  if degrees == "90" {
    videoFilters = "transpose=1"
  } else if degrees == "180" {
    videoFilters = "transpose=2,transpose=2"
  } else if degrees == "270" {
    videoFilters = "transpose=2"
  }

  fmt.Printf("Rotating %s by %s degrees\n", basename, degrees)

  // Create a thumbnail
  thumbPath := "/tmp/" + basename + "_thumb.jpg"
  cmd := exec.Command("ffmpeg",
    "-i", fmt.Sprintf("http://s3.amazonaws.com/%s/%s/%s_360.mp4",
        config.BucketName,
        basename,
        basename,
        ),
    "-vframes", "1",
    "-vf", videoFilters,
    thumbPath,
  )
  err := cmd.Run();
  if err != nil {
    fmt.Printf("Could not generate thumbnail: %v\n", err)
    return
  }
  fmt.Printf("Thumbnail complete: %s\n", thumbPath)
  uploadVideoFile(thumbPath, basename)  
  
  // Get the existing metadata and set the Status to Processing
  s3Bucket := getS3Bucket()
  data, _ := s3Bucket.Get(basename + "/metadata.json")
  var metadata VideoMetadata
  json.Unmarshal(data, &metadata)
  metadata.Status = "Processing"
  jsonMetadata, _ := json.Marshal(metadata)
  _ = getS3Bucket().Put("/" + basename + "/metadata.json",
      []byte(jsonMetadata), "text/json", s3.PublicRead)
  fmt.Printf("Processing metadata written\n")

  fmt.Fprintf(w, "Rotating");

  // NOTE: Do video rotations in a goroutine
  go func() {
    for _, size := range [...]string{"1080", "720", "360"} {
      videoPath := "/tmp/" + basename + "_" + size + ".mp4"
      cmd := exec.Command("ffmpeg", 
          "-i", fmt.Sprintf("http://s3.amazonaws.com/%s/%s/%s_%s.mp4",
              config.BucketName,
              basename,
              basename, 
              size,
              ),
          "-y",
          "-vf", videoFilters,
          "-metadata:s:v:0", "rotate=0",
          "-vcodec", "libx264",
          "-acodec", "copy",
          videoPath,
      )
      ffmpegMutex.Lock()
      cmd.Stdout = os.Stdout
      cmd.Stderr = os.Stderr
      err := cmd.Run()
      ffmpegMutex.Unlock()
      if err != nil {
        fmt.Printf("Could not transcode file: %v\n", err)
        return
      }

      uploadVideoFile(videoPath, basename)
      os.RemoveAll(videoPath)
      fmt.Printf("Rotate %s complete\n", size)
    }

    metadata.Status = "Ready"
    jsonMetadata, _ = json.Marshal(metadata)
    _ = getS3Bucket().Put("/" + basename + "/metadata.json", 
        []byte(jsonMetadata), "text/json", s3.PublicRead)
    fmt.Printf("Final metadata written\n")
  }()
}

func stripRotateTag(w http.ResponseWriter, r *http.Request) {
  vars := mux.Vars(r)
  basename := vars["id"]

  // Get the existing metadata and set the Status to Processing
  s3Bucket := getS3Bucket()
  data, _ := s3Bucket.Get(basename + "/metadata.json")
  var metadata VideoMetadata
  json.Unmarshal(data, &metadata)
  metadata.Status = "Processing"
  jsonMetadata, _ := json.Marshal(metadata)
  _ = getS3Bucket().Put("/" + basename + "/metadata.json",
      []byte(jsonMetadata), "text/json", s3.PublicRead)
  fmt.Printf("Processing metadata written\n")

  fmt.Fprintf(w, "Stripping rotate tag");

  go func() {
    for _, size := range [...]string{"1080", "720", "360"} {
      videoPath := "/tmp/" + basename + "_" + size + ".mp4"
      cmd := exec.Command("ffmpeg", 
          "-i", fmt.Sprintf("http://s3.amazonaws.com/%s/%s/%s_%s.mp4",
              config.BucketName,
              basename,
              basename, 
              size,
              ),
          "-y",
          "-metadata:s:v:0", "rotate=0",
          "-vcodec", "copy",
          "-acodec", "copy",
          videoPath,
      )
      ffmpegMutex.Lock()
      cmd.Stdout = os.Stdout
      cmd.Stderr = os.Stderr
      err := cmd.Run()
      ffmpegMutex.Unlock()
      if err != nil {
        fmt.Printf("Could not transcode file: %v\n", err)
        return
      }

      uploadVideoFile(videoPath, basename)
      os.RemoveAll(videoPath)
      fmt.Printf("Rotate %s complete\n", size)
    }

    metadata.Status = "Ready"
    jsonMetadata, _ = json.Marshal(metadata)
    _ = getS3Bucket().Put("/" + basename + "/metadata.json", 
        []byte(jsonMetadata), "text/json", s3.PublicRead)
    fmt.Printf("Final metadata written\n")
  }()
}


