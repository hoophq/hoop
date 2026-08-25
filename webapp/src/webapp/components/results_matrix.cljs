(ns webapp.components.results-matrix
  "Parses tab separated output into the JS arrays AG Grid and the CSV/JSON
  writers consume. Memoized: it used to re-parse on every render (EVL-121)."
  (:require ["papaparse" :as papa]))

(defonce ^:private cache (atom nil))

(defn- parse [response]
  (when (some? response)
    (let [data (.-data (papa/parse response #js {:delimiter "\t"}))]
      {:matrix data
       :heads (aget data 0)
       :body (.slice data 1)})))

(defn parse-results
  "Returns {:matrix :heads :body} of JS arrays for `response`, or nil.
  Reference stable, so Reagent can skip re-rendering the grid."
  [response]
  (let [cached @cache]
    (if (and cached (= (:key cached) response))
      (:parsed cached)
      (let [parsed (parse response)]
        (reset! cache {:key response :parsed parsed})
        parsed))))

(defn rows?
  "Whether the output has rows below the header, without parsing it.
  papaparse emits one row per line, so this is just a second line."
  [response]
  (boolean (and response (.includes response "\n"))))
