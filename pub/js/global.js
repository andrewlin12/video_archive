var va = {};
va.templates = {};
va.processingVideoIds = {};

va.randomPlaylistIndex = 0;
va.randomPlaylist = [];

va.checkProcessingVideos = function() {
  _.each(_.keys(va.processingVideoIds), function(id, callback) {
    $.get("/video/" + id, function(data) {
      if (data.Status === 'Ready') {
        va.renderVideo(va.processingVideoIds[id], id, data);
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

va.playRandom = function() {
  var id = va.randomPlaylist[va.randomPlaylistIndex % 
      va.randomPlaylist.length];
  va.randomPlaylistIndex++;
  va.playVideo(id);
};

va.playVideo = function(id) {
  var videoElem = $("#video_" + id);
  $("#player").attr(
    "src", videoElem.find("a").attr("href")).attr(
    "controls", "controls");
  $("#player")[0].load();
  $("#player")[0].play();
  var metadata = videoElem.data("metadata");
  $("#player_title").html(metadata.Title);
  $("#player_description").html(metadata.Description);
  $("#player_container").show();
};

va.closePlayer = function() {
  $("#player")[0].pause();
  $("#player").attr("src", "");
  $("#player_container").hide();
}

va.fetchVideos = function() {
  va.randomPlaylist = [];
  va.randomPlaylistIndex = 0;
  va.fetchVideosInternal(0, true);
};

va.fetchVideosInternal = function(skip, getAll) {
  $.get("/videos?skip=" + skip + "&limit=50", function(data) {
    var render = function() {
      if (!va.documentReady) {
        setTimeout(render, 250);
        return;
      }

      var keys = _.keys(data.Ids).sort().reverse();
      _.each(keys, function(k) {        
        var videoData = JSON.parse(data.Ids[k]);
        if (videoData.Status === "Ready") {
          va.randomPlaylist.push(k);
        }
        va.renderVideo(data.Bucket, k, videoData);
      });
      va.randomPlaylist = _.shuffle(va.randomPlaylist);

      if (getAll && data.Remaining > 0) {
        va.fetchVideosInternal(skip + keys.length, true);
      }
    };

    render();
  });
};

va.getProcessingVideosContainer = function() {
  var containerId = "container_processing";
  var container = $("#"+ containerId);
  if (container.length === 0) {
    container = $("<div id='" + containerId + "'><h2></h2></div>").addClass("video-container")
    container.find("h2").html("Processing Videos");
    $("#videos").prepend(container);
  }

  return container;
};

va.renderVideo = function(bucket, id, data) {
  var dateTaken = new Date(parseInt(id.split("_")[0], 10) * 1000);
  var year = dateTaken.getYear();
  var month = dateTaken.getMonth();
  var container = null;
  if (data.Status !== "Ready") {
    container = va.getProcessingVideosContainer();
  } else {
    var containerId = data.Status !== "Ready" ? "container_processing" : 
        "container_" + year + "_" + month;
    container = $("#" + containerId);
    if (container.length === 0) {
      container = $("<div id='" + containerId + "'><h2></h2></div>").addClass("video-container")
      container.find("h2").html(dateTaken.getMonthName() + " " + (1900 + year));
      $("#videos").append(container);
    }

    var navYearId = "nav_year_" + year;
    var navYear = $("#" + navYearId);
    if (navYear.length === 0) {
      navYear = $("<div id='" + navYearId + "'>" + 
          "<a href='javascript:va.toggleYear(\"" + navYearId + "\")'>" +
          (1900 + year) +
          "</a>" +
          "</div>").addClass("nav-year");
      if ($("#time_nav").find(".nav-year").length === 0) {
        $("#time_nav").html("");
      }
      $("#time_nav").append(navYear);
    }

    var navMonthId = "nav_month_" + year + "_" + month;
    var navMonth = $("#" + navMonthId);
    if (navMonth.length === 0) {
      navMonth = $("<div id='" + navMonthId + "'>" + 
          "<a href='javascript:va.gotoMonth(\"" + containerId + "\")'>" +
          dateTaken.getMonthName() +
          "</a>" +
          "</div>").addClass("nav-month");
      navYear.append(navMonth);
    }
  }

  var existing = $("#video_" + id);
  var rendered = va.templates.video({
      bucket: bucket,
      id: id,
      cacheVersion: $.cookie("cacheVersion") || "0"
  });
  if (existing.length > 0) {
    existing.replaceWith(rendered);
  } else {
    container.append(rendered);
  }
  $("#video_" + id).data("bucket", bucket);
  $("#video_" + id).data("metadata", data);

  va.updateVideoStatus(id, data);
}

va.updateVideoStatus = function(id, data) {
  delete va.processingVideoIds[id];
  $("#video_" + id + " .title").html(data.Title);
  $("#video_" + id + " .description").html(data.Description);
  $("#video_" + id + " .duration").html(
      "(" + va.durationToString(data.Duration) + ")");
  $("#video_" + id + " .tools").hide();
  if (data.Status !== 'Ready') {
    $("#video_" + id + " .links").css('display', 'none');
    $("#video_" + id + " .status").html("Videos processing...").show();
    $("#video_" + id + " .links a").attr("href", 
        "javascript:alert('Video not ready yet')");
    va.processingVideoIds[id] = $("#video_" + id).data("bucket");
  } else {
    $("#video_" + id + " .links").css('display', 'inline-block');
    $("#video_" + id + " .status").hide();
  }
};

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

va.rotateVideo = function(id, degrees) {
  $.get("/video/" + id + "/rotate/" + degrees, function(data) {
    $.cookie("cacheVersion", new Date().getTime());
    $.get("/video/" + id, function(data) {
      va.updateVideoStatus(id, data);
    });
  });
};

va.stripRotateTag = function(id, degrees) {
  $.get("/video/" + id + "/stripRotateTag", function(data) {
    $.cookie("cacheVersion", new Date().getTime());
    $.get("/video/" + id, function(data) {
      va.updateVideoStatus(id, data);
    });
  });
};

va.deleteVideo = function(id, degrees) {
  if (window.confirm("Are you sure you want to delete this video?")) {
    $.get("/video/" + id + "/delete", function(data) {
      $("#video_" + id).remove();
    });
  }
};

va.toggleEdit = function(id) {
  $("#video_" + id + " .tools").toggle();
};

va.toggleYear = function(navYearId) {
  $("#" + navYearId).find(".nav-month").toggle();
}

va.gotoMonth = function(containerId) {
  var targetContainer = $("#" + containerId);
  $("body").animate({
    scrollTop: Math.round(targetContainer.position().top) + $("#main").scrollTop()
  });
}

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
  r.assignDrop(document.getElementById('main'));
  r.on('fileAdded', function(file, event){
    va.getProcessingVideosContainer().append(va.templates.uploading_video({
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
    $("#uploading_" + file.uniqueIdentifier).remove();
    va.fetchVideos();
  });
  r.on('fileError', function(file) {
    $("#uploading_" + file.uniqueIdentifier + " .progress").html("Error");
  });

  $("#player")[0].addEventListener("ended", function() {
    va.playRandom();
  });

  setInterval(va.checkProcessingVideos, 15000);
});
va.prepareTemplates();
va.fetchVideos();

Date.prototype.getMonthName = function(lang) {
    lang = lang && (lang in Date.locale) ? lang : 'en';
    return Date.locale[lang].month_names[this.getMonth()];
};

Date.prototype.getMonthNameShort = function(lang) {
    lang = lang && (lang in Date.locale) ? lang : 'en';
    return Date.locale[lang].month_names_short[this.getMonth()];
};

Date.locale = {
    en: {
       month_names: ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'],
       month_names_short: ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
    }
};
