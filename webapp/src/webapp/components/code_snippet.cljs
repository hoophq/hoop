(ns webapp.components.code-snippet
  (:require ["lucide-react" :refer [Copy]]
            [re-frame.core :as rf]
            [webapp.components.headings :as h]))

(defn main
  "config is a map with the following fields:
  :code -> the actual string with the code snippet"
  [config title]
    [:div {:class "overflow-auto"}
     (when title [h/h3 title {:class "mb-regular"}])
     [:section
      {:class (str "relative font-mono overflow-auto "
                   " whitespace-pre text-sm rounded-xl"
                   " p-regular group")
       :style {:backgroundColor "var(--gray-12)"
               :color "var(--gray-1)"}}
      [:div {:class (str "absolute rounded-lg p-x-small "
                         "top-2 right-2 cursor-pointer box-border "
                         "opacity-0 group-hover:opacity-100 transition z-20")
             :on-click #(do
                          (js/navigator.clipboard.writeText (:code config))
                          (rf/dispatch [:show-snackbar
                                        {:level :success
                                         :text "Text copied to clipboard"}]))}
       [:> Copy {:size 14
                 :color "white"}]]
      [:div
       {:class (str (when (:classes config) (:classes config))
                    " overflow-auto "
                    (when (:fixed-height? config) " max-h-80"))}
       (:code config)]]])
