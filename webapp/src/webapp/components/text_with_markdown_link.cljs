(ns webapp.components.text-with-markdown-link
  (:require
   ["@radix-ui/themes" :refer [Text Link Box]]
   [clojure.string :as cs]))

(defn main
  "Renders text with markdown links converted to components"
  [text & [text-props link-props]]
  (let [markdown-link-pattern #"\[([^\]]+)\]\(([^)]+)\)"
        has-links? (re-find markdown-link-pattern text)]

    (if-not has-links?
      [:> Text text-props text]

      ;; Estratégia: split por regex capturadora
      (let [parts (cs/split text #"(\[[^\]]+\]\([^)]+\))")]

        (into [:div {:class "inline"}]
              (map-indexed
               (fn [_idx part]
                 (if (re-matches markdown-link-pattern part)
                   ;; É um link
                   (let [[_ link-text link-url] (re-find markdown-link-pattern part)]
                     [:> Link (merge {:href link-url
                                      :target "_blank"}
                                     link-props)
                      link-text])
                   ;; É texto normal
                   (when (pos? (count part))
                     [:> Text text-props part])))
               parts))))))
