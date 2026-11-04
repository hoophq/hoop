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
  "Select options for the agents, sorted by name and narrowed to names that
  contain `search` (case-insensitive). The selected agent stays in the list,
  so the select keeps showing it while a search hides the others."
  [agents search selected-id]
  (let [term (cs/lower-case (cs/trim (or search "")))]
    (->> agents
         (filter #(or (= (:id %) selected-id)
                      (cs/blank? term)
                      (cs/includes? (cs/lower-case (or (:name %) "")) term)))
         (sort-by :name compare-names)
         (mapv #(hash-map :value (:id %)
                          :text (:name %))))))
