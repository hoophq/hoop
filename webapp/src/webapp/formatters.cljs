(ns webapp.formatters
  (:require
   ["date-fns" :as dfns]
   [clojure.string :as string]))

(defn comma-string-to-list
  "Transform a comma separated string to list"
  [roles]
  (if (empty? roles) []
      (string/split
       (string/replace roles #", | , " ",")
       #",")))

(defn list-to-comma-string
  "Transform a list into a comma separated string"
  [roles]
  (string/join ", " roles))

(defn replace-empty-space->dash
  [string]
  (string/replace string #"\s" "-"))

(defn time-elapsed
  "PARAMETERS
  time -> a value in miliseconds

  Returns a string containing a human readable value of time.
  For instance:
  - Less than a second
  - 26 seconds;
  - 10 minutes;
  - in case of hours, if it has at least 1 minute, it returns 'X hours and Y minutes', otherwise, it returns 'X hours' only
  "
  [time]
  (let [time-in-seconds (/ time 1000)
        units [{:name "second" :limit 60 :in-second 1}
               {:name "minute" :limit 3600 :in-second 60}
               {:name "hour" :limit 9999999999 :in-second 3600}]
        unit (first (drop-while #(>= time-in-seconds (:limit %))
                                units))]
    (cond
      ;; In the miliseconds
      (< time-in-seconds 1)
      "less than a second"
      ;; Simpler response in case the response is in less than an hour
      (and (>= time-in-seconds 1) (< time-in-seconds 3600))
      (-> (/ time-in-seconds (:in-second unit))
          Math/floor
          int
          (#(str % " " (:name unit) (when (> % 1) "s"))))
      ;; Response has more than one hour
      :else (let [t (/ time-in-seconds (:in-second unit))
                  hours-time (int (Math/floor t))
                  minutes-time (int (Math/floor (* (- t hours-time) 60)))]
              (str hours-time " " (:name unit) (when (> hours-time 1) "s")
                   (when (>= minutes-time 1)
                     (str " and " minutes-time " minute"
                          (when (> minutes-time 1) "s"))))))))

(defn current-time []
  (let [hours (-> (new js/Date)
                  (.getHours)
                  (#(str (if (< % 10) (str "0" %) %))))
        minutes (-> (new js/Date)
                    (.getMinutes)
                    (#(str (if (< % 10) (str "0" %) %))))
        seconds (-> (new js/Date)
                    (.getSeconds)
                    (#(str (if (< % 10) (str "0" %) %))))]
    (str "[" hours ":" minutes ":" seconds "]")))

(defn time-ago
  "DEPRECATED
  It receives our Hoop API date format, a simple string containing YYYY/MM/DD HH:MM
  and parses to a readable string containing `x time ago`, for instance:
  - 10 minutes ago
  - 1 hour ago

  Important: `time` parameters will always be assumed as UTC timezone, so make sure you're passing a UTC timezone date formatted as `YYYY/MM/DD HH:MM` in here.
  "
  [time]
  (let [units [{:name "second" :limit 60 :in-second 1}
               {:name "minute" :limit 3600 :in-second 60}
               {:name "hour" :limit 86400 :in-second 3600}
               {:name "day" :limit 604800 :in-second 86400}
               {:name "week" :limit 2629743 :in-second 604800}
               {:name "month" :limit 31556926 :in-second 2629743}
               {:name "year" :limit 99999999999999 :in-second 31556926}]
        ts (/ (.parse js/Date (str time " UTC")) 1000)
        now (/ (.getTime (new js/Date)) 1000)
        diff (- now ts)]
    (if (< diff 30)
      "just now"
      (let [unit (first (drop-while #(or (>= diff (:limit %))
                                         (not (:limit %)))
                                    units))]
        (-> (/ diff (:in-second unit))
            Math/floor
            int
            (#(str % " " (:name unit) (when (> % 1) "s") " ago")))))))

(defn time-ago-full-date
  "It receives our hoop API date format, a simple string containing 2022-10-28T16:09:17.772Z
  and parses to a readable string containing `x time ago`, for instance:
  - 10 minutes ago
  - 1 hour ago"
  [time]
  (let [units [{:name "second" :limit 60 :in-second 1}
               {:name "minute" :limit 3600 :in-second 60}
               {:name "hour" :limit 86400 :in-second 3600}
               {:name "day" :limit 604800 :in-second 86400}
               {:name "week" :limit 2629743 :in-second 604800}
               {:name "month" :limit 31556926 :in-second 2629743}
               {:name "year" :limit 99999999999999 :in-second 31556926}]
        ts (/ (.parse js/Date time) 1000)
        now (/ (.getTime (new js/Date)) 1000)
        diff (- now ts)]
    (if (< diff 30)
      "just now"
      (let [unit (first (drop-while #(or (>= diff (:limit %))
                                         (not (:limit %)))
                                    units))]
        (-> (/ diff (:in-second unit))
            Math/floor
            int
            (#(str % " " (:name unit) (when (> % 1) "s") " ago")))))))

(defn time-parsed->full-date
  "It receives our hoop API date format, a simple string containing 2022-10-28T16:09:17.772Z
  and parses to a readable string containing `DD/MM/YYYY hh:mm:ss`, for instance:
  - 05/06/2023 14:55:18"
  [time]
  (let [date (new js/Date time)
        insert-0-before (fn [number]
                          (-> number
                              (#(str "0" %))
                              (#(take-last 2 %))
                              string/join))]
    (str (insert-0-before (.getDate date)) "/"
         (insert-0-before (+ 1 (.getMonth date))) "/"
         (.getFullYear date) " "
         (insert-0-before (.getHours date)) ":"
         (insert-0-before (.getMinutes date)) ":"
         (insert-0-before (.getSeconds date)))))

;; Time window helpers for UTC conversion using date-fns

(defn local-time->utc-time
  "Converts local time string (HH:mm format) to UTC time string (HH:mm format).

  Example:
  (local-time->utc-time \"09:00\") => \"12:00\" (if user is in UTC-3)

  Parameters:
  - local-time-str: String in format \"HH:mm\" representing local time"
  [local-time-str]
  (when (and local-time-str (> (count local-time-str) 0))
    (let [now (js/Date.)
          ;; Create a date with today's date and the selected time in local timezone
          [hours minutes] (map js/parseInt (string/split local-time-str #":"))
          local-date (js/Date. (.getFullYear now)
                               (.getMonth now)
                               (.getDate now)
                               hours
                               minutes)
          ;; Get UTC hours and minutes from the local date
          utc-hours (.getUTCHours local-date)
          utc-minutes (.getUTCMinutes local-date)]
      ;; Format as HH:mm directly (date-fns format uses local timezone, so we format manually)
      (str (if (< utc-hours 10) "0" "") utc-hours ":"
           (if (< utc-minutes 10) "0" "") utc-minutes))))

(defn utc-time->display-time
  "Converts UTC time string (HH:mm format) to 12-hour format with AM/PM for display.

  Example:
  (utc-time->display-time \"22:00\") => \"10:00 PM\"
  (utc-time->display-time \"09:00\") => \"9:00 AM\"

  Parameters:
  - utc-time-str: String in format \"HH:mm\" representing UTC time"
  [utc-time-str]
  (when (and utc-time-str (> (count utc-time-str) 0))
    (let [now (js/Date.)
          [hours minutes] (map js/parseInt (string/split utc-time-str #":"))
          ;; Create a UTC date for today with the specified time
          utc-date (js/Date. (js/Date.UTC (.getUTCFullYear now)
                                          (.getUTCMonth now)
                                          (.getUTCDate now)
                                          hours
                                          minutes))]
      ;; Format as 12-hour time with AM/PM using date-fns
      (dfns/format utc-date "h:mm a"))))

(defn is-within-time-window?
  "Checks if current UTC time is within the specified time window.

  Parameters:
  - start-time-utc: String in format \"HH:mm\" representing UTC start time
  - end-time-utc: String in format \"HH:mm\" representing UTC end time

  Returns:
  - Boolean indicating if current UTC time is within the window"
  [start-time-utc end-time-utc]
  (when (and start-time-utc end-time-utc (> (count start-time-utc) 0) (> (count end-time-utc) 0))
    (let [now (js/Date.)
          ;; Get current UTC time directly (not using date-fns format which uses local timezone)
          current-utc-hours (.getUTCHours now)
          current-utc-minutes (.getUTCMinutes now)
          current-minutes (+ (* current-utc-hours 60) current-utc-minutes)
          ;; Parse start and end times
          start-parts (string/split start-time-utc #":")
          end-parts (string/split end-time-utc #":")
          start-hours (js/parseInt (first start-parts))
          start-minutes (js/parseInt (second start-parts))
          end-hours (js/parseInt (first end-parts))
          end-minutes (js/parseInt (second end-parts))
          start-total-minutes (+ (* start-hours 60) start-minutes)
          end-total-minutes (+ (* end-hours 60) end-minutes)]
      (and (>= current-minutes start-total-minutes)
           (<= current-minutes end-total-minutes)))))
