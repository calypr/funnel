
function formatElapsedTime(miliseconds) {
  var days, hours, minutes, seconds, total_hours, total_minutes, total_seconds;

  total_seconds = parseInt(Math.floor(miliseconds / 1000));
  total_minutes = parseInt(Math.floor(total_seconds / 60));
  total_hours = parseInt(Math.floor(total_minutes / 60));

  seconds = parseInt(total_seconds % 60);
  minutes = parseInt(total_minutes % 60);
  hours = parseInt(total_hours % 24);
  days = parseInt(Math.floor(total_hours / 24));

  var time = "";
  if (days > 0) {
    time += days + "d ";
  }
  if (hours > 0 || days > 0) {
    time += hours + "h ";
  }
  if (minutes > 0 || hours > 0) {
    time += minutes + "m ";
  }
  if (seconds > 0 || minutes > 0) {
    time += seconds + "s";
  }
  if (time === "") {
    time = "< 1s";
  }
  return time;
}

function elapsedTime(task) {
  if (task.logs && task.logs.length) {
    if (task.logs[0].startTime) {
      var now = new Date();
      if (isDone(task)) {
        if (task.logs[0].endTime) {
          now = Date.parse(task.logs[0].endTime);
        } else {
          return "--";
        }
      }
      var started = Date.parse(task.logs[0].startTime);
      var elapsed = now - started;
      return formatElapsedTime(elapsed);
    }
  }
  return;
}

function formatTimestamp(utcNanoseconds) {
  if (utcNanoseconds) {
    var utcSeconds = parseInt(utcNanoseconds) / 1000000;
    var date = new Date(utcSeconds);
    var options = {
      weekday: "short",
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
    };
    return date.toLocaleDateString("en-US", options);
  }
  return "--";
}

function formatDate(tstamp) {
  if (tstamp) {
    var date = new Date(tstamp);
    var options = {
      weekday: "short",
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
    };
    return date.toLocaleDateString("en-US", options);
  }
  return "--";
}

function isDone(task) {
  if (task.state === undefined) {
    return false;
  }
  return (
    task.state === "COMPLETE" ||
    task.state === "EXECUTOR_ERROR" ||
    task.state === "CANCELED" ||
    task.state === "SYSTEM_ERROR"
  );
}

// Initialises request headers for future requests.
// If authentication is required, immediately redirects to '/login'.
const fetchOptsPromise = fetch("/login/token")
  .then((response) => {
    if (response.status !== 200) {
      throw new Error("User authentication is required");
    }
    return response.text();
  })
  .then((token) => {
    const fetchOpts = {};
    if (token) fetchOpts.headers = { Authorization: "Bearer " + token };
    return fetchOpts;
  })
  .catch((_) => (window.location.pathname = "/login"));

// Updates fetch API configuration for POST requests.
function fetchWithPost(fetchOpts, doPost) {
  if (!doPost) return fetchOpts;
  return {
    method: "POST",
    headers: {
      ...(fetchOpts.headers || {}),
      Accept: "application/json",
      "Content-Type": "application/json",
    },
  };
}

// Retrieves data from given URL. Usually, it's a GET-request.
// When postJson is true, it's a POST-request without body.
// Returns the Promise from the fetch() call.
function get(url, postJson) {
  if (!url instanceof URL) {
    throw new Error("Expected URL object; got: " + typeof url);
  } else if (url.pathname.includes("/v1/tasks")) {
    url.searchParams.set("view", "FULL");
  }

  return fetchOptsPromise
    .then((fetchOpts) => fetchWithPost(fetchOpts, postJson))
    .then((fetchOpts) => fetch(url.toString(), fetchOpts))
    .then((response) => {
      // A 204 No Content (e.g. a successful task cancel) has an empty body, so
      // response.json() would throw. Return null instead of trying to parse.
      if (response.status === 204) {
        return null;
      }
      return response.json();
    })
    .catch((error) => {
      console.log("get", url.toString(), "error:", error);
      throw error;
    },
  );
}

// Performs POST-request to given URL without payload
// Returns the Promise from the fetch() call.
function post(url) {
  return get(url, true);
}

export { isDone, formatDate, formatTimestamp, elapsedTime, get, post };
