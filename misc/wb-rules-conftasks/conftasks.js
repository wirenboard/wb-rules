/* wb-rules-conftasks - allows to define tasks (i.e. rules) programatically
based on the description from JSON config file.

Plese see details in the example config and schema file
*/


(function() { //create namespace
  /* parseDate(date) creates new Date object.
   * date is parsed according to ISO 8601.
   * In contrast to ES5 standard, the date string with absent timezone
   * is treated as local time.
   * Parser accepts space instead of T as date and time delimiter.
   *
   * Examples:
   *  parseDate("2016-06-01T10:00:00")  => 2016-06-01 10:00:00.000+03:00
   *  parseDate("2016-06-01T10:00:00Z") => 2016-06-01 13:00:00.000+03:00
   *  new Date("2016-06-01T10:00:00")   => 2016-06-01 13:00:00.000+03:00
   *
   * based on https://github.com/csnover/js-iso8601
  */

  var parseDate = (function() {
      var numericKeys = [ 1, 4, 5, 6, 7, 10, 11 ];
      return function(date) {
          var result, struct, minutesOffset = 0;
          //              1 YYYY                2 MM       3 DD           4 HH    5 mm       6 ss        7 msec        8 Z 9 ±    10 tzHH    11 tzmm
          if ((struct = /^(\d{4}|[+\-]\d{6})(?:-(\d{2})(?:-(\d{2}))?)?(?:[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{3}))?)?(?:(Z)|([+\-])(\d{2})(?::(\d{2}))?)?)?$/.exec(date))) {
              // avoid NaN timestamps caused by “undefined” values being passed to Date.UTC
              log(struct);
              for (var i = 0, k; (k = numericKeys[i]); ++i) {
                  struct[k] = +struct[k] || 0;
              }

              // allow undefined days and months
              struct[2] = (+struct[2] || 1) - 1;
              struct[3] = +struct[3] || 1;

              if ((struct[8] == 'Z') || struct[9] !== undefined) {
                  // timezone is explicitly set
                  minutesOffset = struct[10] * 60 + struct[11];

                  if (struct[9] === '+') {
                      minutesOffset = 0 - minutesOffset;
                  }

                  return new Date(Date.UTC(struct[1], struct[2], struct[3], struct[4], struct[5] + minutesOffset, struct[6], struct[7]));
              } else {
                  // no timezone information, assume local time
                  return new Date(struct[1], struct[2], struct[3], struct[4], struct[5], struct[6], struct[7]);
              }
          } else {
              // cannot parse as ISO8601 string, falling back to built-in parser
              return new Date(date);
          }
      };
  })();

  var config = readConfig("/etc/wb-rules-tasks.conf");
  var excluded_channels = config.excluded_channels;
  var schedules = config.schedules;
  schedules.forEach(function(schedule,schedule_index) {
    if (schedule.name == undefined) {
      schedule.name = "anon{}".format(schedule_index);
    }

    schedule.start_date = parseDate(schedule.start_date);
    schedule.end_date = parseDate(schedule.end_date);

    log("creating schedule {} active from {} till {}".format(
      schedule.name, schedule.start_date, schedule.end_date));

    var tasks = schedule.tasks;
    tasks.forEach(function(task, task_index) {
      if (task.name == undefined) {
        task.name = "anon{}".format(task_index);
      }

      if ((task.enabled != undefined) && (!task.enabled)) {
        log("skiping disabled task {}".format(task.name));
        return;
      }

      log("creating task: name: {}, run_at: {}, as_soon_as: {}, when: {}, if: {}".format(task.name, task.run_at, task.as_soon_as, task.when, task.if));
      task.actions = task.actions.filter(function(action) {
        return (excluded_channels.indexOf(action.channel) == -1);
      });

      var task_params = {};
      if (task.run_at) {
        task_params.when = cron(task.run_at);
      }

      if (task.when) {
        task_params.when = new Function('return ' + task.when);;
      }

      if (task.if) {
        task.condition = new Function('return ' + task.if);
      }

      if (task.as_soon_as) {
        task_params.asSoonAs = new Function('return ' + task.as_soon_as);
      }


      task_params.then = function() {
        var now = new Date();
        if ((now >= schedule.start_date) && (now < schedule.end_date)) {
          if (task.condition && !task.condition()) {
            return;
          }
          debug("running task {}/{}".format(schedule.name, task.name));
          task.actions.forEach(function(action) {
            if (action.set_value != undefined) {
              log("set {} => {}", action.channel, action.set_value);
              dev[action.channel] = action.set_value;
            }
          });
        }
      };

      defineRule("_scheduler_rule_{}_{}".format(schedule_index, task_index), task_params);
    });
  });
})();


