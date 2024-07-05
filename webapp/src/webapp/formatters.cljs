(ns webapp.formatters
  (:require [clojure.string :as string]))

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
