(ns webapp.components.results-matrix-test
  "Reference stability is a contract: the grid only skips a re-render when its
  props are identical (EVL-121)."
  (:require
   [cljs.test :refer-macros [deftest testing is]]
   [webapp.components.results-matrix :as results-matrix]))

(def ^:private response "id\tname\n1\talice\n2\tbob\n")

(deftest parses-heads-and-body
  (let [{:keys [matrix heads body]} (results-matrix/parse-results response)]
    (is (= [["id" "name"] ["1" "alice"] ["2" "bob"] [""]] (js->clj matrix)))
    (is (= ["id" "name"] (js->clj heads)))
    (is (= [["1" "alice"] ["2" "bob"] [""]] (js->clj body)))))

(deftest nil-response-parses-to-nil
  (is (nil? (results-matrix/parse-results nil))))

(deftest repeated-parse-returns-the-same-arrays
  (let [a (results-matrix/parse-results response)
        b (results-matrix/parse-results response)]
    (is (identical? (:matrix a) (:matrix b)))
    (is (identical? (:heads a) (:heads b)))
    (is (identical? (:body a) (:body b)))))

(deftest a-new-response-invalidates-the-cache
  (let [a (results-matrix/parse-results response)
        b (results-matrix/parse-results "id\n9\n")]
    (is (not (identical? (:matrix a) (:matrix b))))
    (is (= ["id"] (js->clj (:heads b))))))

(deftest rows?-matches-a-full-parse
  (testing "papaparse emits one row per line, blank lines included"
    (doseq [[response expected] {nil false
                                 "" false
                                 "header only" false
                                 "\n" true
                                 "id\tname\n" true
                                 "id\tname\n1\talice" true
                                 "\nid\tname" true
                                 "   \n  " true}]
      (is (= expected (results-matrix/rows? response))
          (pr-str response)))))
