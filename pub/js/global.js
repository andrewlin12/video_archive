var va = {};
va.templates = {};
va.processingVideoIds = {};

va.checkProcessingVideos = function() {
  _.each(_.keys(va.processingVideoIds), function(id, callback) {
    $.get("/video/" + id, function(data) {
      if (data.Status === 'Ready') {
        va.renderVideo(va.processingVideoIds[id], id);
        delete va.processingVideoIds[id];
      }
    });
   }, function(err) {
   });
};

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
      _.each(data.Ids.reverse(), function(id) {
        va.renderVideo(data.Bucket, id);
      });
    };

    render();
  });
};

va.renderVideo = function(bucket, id) {
  var existing = $("#video_" + id);
  var rendered = va.templates.video({
      bucket: bucket,
      id: id
  });
  if (existing.length > 0) {
    existing.replaceWith(rendered);
  } else {
    $("#videos").append(rendered);
  }

  delete va.processingVideoIds[id];
  $.get("/video/" + id, function(data) {
    $("#video_" + id + " .title").html(data.Title);
    $("#video_" + id + " .description").html(data.Description);
    $("#video_" + id + " .duration").html(
        va.durationToString(data.Duration));
    if (data.Status !== 'Ready') {
      $("#video_" + id + " .links").hide();
      $("#video_" + id + " .status").html("Videos processing...").show();
      $("#video_" + id + " a").attr("href", "javascript:alert('Video not ready yet')");
      va.processingVideoIds[id] = bucket;
    } else {
      $("#video_" + id + " .links").show();
      $("#video_" + id + " .status").hide();
    }
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
    },
    simultaneousUploads: 1
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

  setInterval(va.checkProcessingVideos, 15000);
});
va.prepareTemplates();
va.fetchVideos();
