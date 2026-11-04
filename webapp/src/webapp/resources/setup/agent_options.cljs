(ns webapp.resources.setup.agent-options
  (:require
   [clojure.string :as cs]))

(defn- compare-names
  "Case-insensitive, numeric-aware name order (agent2 before agent10). Same
  order as webapp_v2 `compareNames` (src/pages/Agents/filters.js)."
  [a b]
  (.localeCompare (or a "") (or b "") js/undefined
                  #js {:sensitivity "base" :numeric true}))

(defn agent-options
  "Options for the agents, sorted by name and narrowed to names that contain
  `search` (case-insensitive)."
  [agents search]
  (let [term (cs/lower-case (cs/trim (or search "")))]
    (->> agents
         (filter #(or (cs/blank? term)
                      (cs/includes? (cs/lower-case (or (:name %) "")) term)))
         (sort-by :name compare-names)
         (mapv #(hash-map :value (:id %)
                          :text (:name %))))))
