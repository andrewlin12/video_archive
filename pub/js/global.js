var va = {};
va.templates = {};

va.durationToString = function(durationSeconds) {
  var total = parseInt(durationSeconds, 10);
  var secs = total % 60;
  if (secs < 10) {
    secs = '0' + secs;
  }
  var mins = Math.floor((total % 3600) / 60);
  var hours = Math.floor(total / 3600);
  if (hours > 0 && mins < 10) {
    mins = '0' + mins;
  }
  
  var result = mins + ':' + secs;
  if (hours > 0) {
    result = hours + ':' + result;
  }
  return result;
};

va.fetchVideos = function() {
  $.get("/videos", function(data) {
    var render = function() {
      if (!va.documentReady) {
        setTimeout(render, 250);
        return;
      }

      $("#videos").empty();
      _.each(data.Ids, function(id) {
        va.renderVideo(data.Bucket, id);
      });
    };

    render();
  });
};

va.renderVideo = function(bucket, id) {
  $("#videos").prepend(va.templates.video({
    bucket: bucket,
    id: id
  }));

  $.get("/video/" + id, function(data) {
    $("#video_" + id + " .title").html(data.Title);
    $("#video_" + id + " .duration").html(
        va.durationToString(data.Duration));
  });
}

va.prepareTemplates = function() {
  async.each([
    "uploading_video",
    "video"
  ], function(templateName, callback) {
    $.get("/tmpl/" + templateName + ".html", function(data) {
      va.templates[templateName] = _.template(data);
      callback();
    });
  }, function(err) {
    va.templatesLoaded = true;
  });
};

$(function() {    
  va.documentReady = true;

  var r = new Resumable({
    target:'/upload', 
    query: {
      upload_token:'my_token'
    }
  });
  r.assignBrowse(document.getElementById('browseButton'));
  r.on('fileAdded', function(file, event){
    $("#videos").prepend(va.templates.uploading_video({
      filename: file.fileName,
      id: file.uniqueIdentifier
    }));
    r.upload();
  });
  r.on('fileProgress', function(file) {
    $("#uploading_" + file.uniqueIdentifier + " .progress").html(
        Math.round(file.progress() * 100) + "%");
  });
  r.on('fileSuccess', function(file) {
    va.fetchVideos();
  });
  r.on('fileError', function(file) {
    $("#uploading_" + file.uniqueIdentifier + " .progress").html("Error");
  });
});
va.prepareTemplates();
va.fetchVideos();
