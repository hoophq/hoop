(ns webapp.components.log-lines
  "Splits log output into the rows a virtualized list renders. Memoized per
  component, like results-matrix: the split used to run on every render.")

(def ^:const max-line-chars
  "Row virtualization bounds height, not width. One unwrapped line of ~3.8M
  chars still hits Chrome's 2^25 px layout clamp (EVL-121), so a row is cut
  here and marked."
  (* 10 1024))

(defn new-cache
  "One per component, so the rows are released on unmount."
  []
  (atom nil))

(defn humanize-size
  "Formats a character count as a size; log output is effectively ASCII."
  [n]
  (cond
    (>= n (* 1024 1024)) (str (.toFixed (/ n 1024 1024) 1) " MB")
    (>= n 1024) (str (.toFixed (/ n 1024) 1) " KB")
    :else (str n " B")))

(defn clipped? [line]
  (> (count line) max-line-chars))

(defn- strip-cr [line]
  (if (.endsWith line "\r") (subs line 0 (dec (count line))) line))

(defn- split [response]
  (if (and response (seq response))
    ;; Chrome splits 19 MB on a string in 2 ms and on a regex in ~550 ms.
    (loop [rows (vec (.map (.split response "\n") strip-cr))]
      (if (and (pos? (count rows)) (= "" (peek rows)))
        (recur (pop rows))
        rows))
    []))

(defn parse
  "Returns {:rows [line ...] :clipped n} for `response`, where n counts the
  rows above `max-line-chars`. Reference stable across renders."
  [cache response]
  (let [cached @cache]
    (if (and cached (= (:key cached) response))
      (:parsed cached)
      (let [rows (split response)
            parsed {:rows rows
                    :clipped (reduce #(if (clipped? %2) (inc %1) %1) 0 rows)}]
        (reset! cache {:key response :parsed parsed})
        parsed))))

(def ^:const row-height
  "Pixels per row, the virtualized list's `item-height`."
  20)

(defn row
  "One rendered line, `row-height` tall even when empty. A clipped line shows
  its first `max-line-chars` and how much was cut."
  [line class]
  (let [class (str "h-5 leading-5 whitespace-pre " class)]
    (if (clipped? line)
      [:div {:class class}
       (subs line 0 max-line-chars)
       [:span {:class "italic opacity-60 select-none"}
        (str " … " (humanize-size (- (count line) max-line-chars)) " clipped")]]
      [:div {:class class} line])))
