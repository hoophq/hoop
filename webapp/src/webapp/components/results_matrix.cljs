(ns webapp.components.results-matrix
  "Parses tab separated output into the JS arrays AG Grid and the CSV/JSON
  writers consume. Memoized: it used to re-parse on every render (EVL-121)."
  (:require ["papaparse" :as papa]))

(defn new-cache
  "One per component, so the matrix is released on unmount."
  []
  (atom nil))

(defn- parse [response]
  (when (some? response)
    (let [data (.-data (papa/parse response #js {:delimiter "\t"}))]
      {:matrix data
       :heads (aget data 0)
       :body (.slice data 1)})))

(defn parse-results
  "Returns {:matrix :heads :body} of JS arrays for `response`, or nil.
  Reference stable, so Reagent can skip re-rendering the grid."
  [cache response]
  (let [cached @cache]
    (if (and cached (= (:key cached) response))
      (:parsed cached)
      (let [parsed (parse response)]
        (reset! cache {:key response :parsed parsed})
        parsed))))

(defn release-stale!
  "Drops a matrix parsed for an older response. Returns nil."
  [cache response]
  (when-let [cached @cache]
    (when-not (= (:key cached) response)
      (reset! cache nil)))
  nil)

(defn rows?
  "Whether the output has rows below the header, without parsing it."
  [response]
  (boolean (and response (.includes response "\n"))))
