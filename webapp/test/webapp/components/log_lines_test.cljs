(ns webapp.components.log-lines-test
  "Rows are split once per response and no single line reaches layout whole
  (EVL-121, SUP-278)."
  (:require
   [cljs.test :refer-macros [deftest is]]
   [clojure.string :as cs]
   [webapp.components.log-lines :as log-lines]))

(defn- line-of [n] (.repeat "x" n))

(deftest splits-on-lf-and-crlf
  (is (= ["a" "b" "c"]
         (:rows (log-lines/parse (log-lines/new-cache) "a\nb\r\nc\n")))))

(deftest blank-output-has-no-rows
  (doseq [response [nil ""]]
    (is (= {:rows [] :clipped 0}
           (log-lines/parse (log-lines/new-cache) response))
        (pr-str response))))

(deftest repeated-parse-returns-the-same-rows
  (let [cache (log-lines/new-cache)
        a (log-lines/parse cache "a\nb")
        b (log-lines/parse cache "a\nb")]
    (is (identical? a b))))

(deftest a-new-response-invalidates-the-cache
  (let [cache (log-lines/new-cache)
        a (log-lines/parse cache "a\nb")
        b (log-lines/parse cache "c")]
    (is (not (identical? a b)))
    (is (= ["c"] (:rows b)))))

(deftest counts-rows-above-the-line-limit
  (let [long-line (line-of (inc log-lines/max-line-chars))
        {:keys [rows clipped]} (log-lines/parse
                                (log-lines/new-cache)
                                (str "short\n" long-line "\n" long-line))]
    (is (= 3 (count rows)))
    (is (= 2 clipped))))

(deftest a-row-at-the-limit-is-not-clipped
  (let [line (line-of log-lines/max-line-chars)
        [tag attrs text] (log-lines/row line "c")]
    (is (not (log-lines/clipped? line)))
    (is (= :div tag))
    (is (cs/ends-with? (:class attrs) " c"))
    (is (= line text))))

(deftest an-empty-line-keeps-the-row-height
  (let [[_ attrs text] (log-lines/row "" "")]
    (is (cs/includes? (:class attrs) "h-5"))
    (is (= "" text))))

(deftest a-clipped-row-shows-the-limit-and-what-was-cut
  (let [line (line-of (+ log-lines/max-line-chars 2048))
        [tag attrs text marker] (log-lines/row line "c")]
    (is (= :div tag))
    (is (cs/ends-with? (:class attrs) " c"))
    (is (= log-lines/max-line-chars (count text)))
    (is (cs/includes? (last marker) "2.0 KB clipped"))))

(deftest humanize-size
  (is (= "512 B" (log-lines/humanize-size 512)))
  (is (= "1.5 KB" (log-lines/humanize-size 1536)))
  (is (= "15.0 MB" (log-lines/humanize-size (* 15 1024 1024)))))
