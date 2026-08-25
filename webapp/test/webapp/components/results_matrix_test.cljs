(ns webapp.components.results-matrix-test
  "The grid only skips a re-render when its props are identical (EVL-121)."
  (:require
   [cljs.test :refer-macros [deftest testing is]]
   [webapp.components.results-matrix :as results-matrix]))

(def ^:private response "id\tname\n1\talice\n2\tbob\n")

(deftest parses-heads-and-body
  (let [{:keys [matrix heads body]} (results-matrix/parse-results
                                     (results-matrix/new-cache) response)]
    (is (= [["id" "name"] ["1" "alice"] ["2" "bob"] [""]] (js->clj matrix)))
    (is (= ["id" "name"] (js->clj heads)))
    (is (= [["1" "alice"] ["2" "bob"] [""]] (js->clj body)))))

(deftest nil-response-parses-to-nil
  (is (nil? (results-matrix/parse-results (results-matrix/new-cache) nil))))

(deftest repeated-parse-returns-the-same-arrays
  (let [cache (results-matrix/new-cache)
        a (results-matrix/parse-results cache response)
        b (results-matrix/parse-results cache response)]
    (is (identical? (:matrix a) (:matrix b)))
    (is (identical? (:heads a) (:heads b)))
    (is (identical? (:body a) (:body b)))))

(deftest a-new-response-invalidates-the-cache
  (let [cache (results-matrix/new-cache)
        a (results-matrix/parse-results cache response)
        b (results-matrix/parse-results cache "id\n9\n")]
    (is (not (identical? (:matrix a) (:matrix b))))
    (is (= ["id"] (js->clj (:heads b))))))

(deftest caches-do-not-evict-each-other
  (let [one (results-matrix/new-cache)
        two (results-matrix/new-cache)
        a (results-matrix/parse-results one response)]
    (results-matrix/parse-results two "id\n9\n")
    (is (identical? (:matrix a) (:matrix (results-matrix/parse-results one response))))))

(deftest a-skipped-parse-releases-a-replaced-result
  (let [cache (results-matrix/new-cache)
        a (results-matrix/parse-results cache response)]
    (is (nil? (results-matrix/release-stale! cache "id\n9\n")))
    (is (not (identical? (:matrix a)
                         (:matrix (results-matrix/parse-results cache response)))))))

(deftest a-skipped-parse-keeps-the-result-on-screen
  (let [cache (results-matrix/new-cache)
        a (results-matrix/parse-results cache response)]
    (results-matrix/release-stale! cache response)
    (is (identical? (:matrix a)
                    (:matrix (results-matrix/parse-results cache response))))))

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
